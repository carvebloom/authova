package transport

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/api/authova/wire"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	"github.com/carvebloom/authova/internal/protojsoncompat"
	"github.com/carvebloom/authova/internal/resource"
	ktransport "github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type bodyDecodeError struct{}

// Body errors are deferred to preserve current operator/role admission order.
func decodeProtoBody(r *http.Request, target any) error {
	var err error
	media, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil || media != "application/json" {
		err = domain.ErrInvalid
	} else {
		var data []byte
		data, err = io.ReadAll(r.Body)
		if err == nil {
			value := reflect.ValueOf(target)
			if value.Kind() == reflect.Pointer && value.Elem().Kind() == reflect.Pointer {
				body := reflect.New(value.Elem().Type().Elem())
				message, ok := body.Interface().(proto.Message)
				if ok {
					value.Elem().Set(body)
					err = protojsoncompat.Unmarshal(data, message)
				} else {
					err = domain.ErrInvalid
				}
			} else {
				err = domain.ErrInvalid
			}
		}
	}
	if err != nil {
		*r = *r.WithContext(context.WithValue(r.Context(), bodyDecodeError{}, domain.ErrInvalid))
	}
	return nil
}
func protoSuccessStatus(ctx context.Context) int {
	tr, ok := ktransport.FromServerContext(ctx)
	if !ok {
		return 200
	}
	operation := strings.TrimPrefix(tr.Operation(), "/")
	i := strings.LastIndex(operation, "/")
	if i < 0 {
		return 200
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(operation[:i] + "." + operation[i+1:]))
	if err != nil {
		return 200
	}
	method, ok := d.(protoreflect.MethodDescriptor)
	if !ok || !proto.HasExtension(method.Options(), wire.E_Operation) {
		return 200
	}
	var metadata struct{ Responses map[string]any }
	if json.Unmarshal([]byte(proto.GetExtension(method.Options(), wire.E_Operation).(string)), &metadata) != nil {
		return 200
	}
	for code := range metadata.Responses {
		if strings.HasPrefix(code, "2") {
			n, err := strconv.Atoi(code)
			if err == nil {
				return n
			}
		}
	}
	return 200
}

type inventoryService struct{ server *Server }

var _ v1.AppInventoryHTTPServer = (*inventoryService)(nil)

func (s *inventoryService) authorize(ctx context.Context, write bool) (context.Context, *http.Request, domain.Operator, error) {
	r, ok := khttp.RequestFromServerContext(ctx)
	if !ok {
		return ctx, nil, domain.Operator{}, domain.ErrUnavailable
	}
	if meta := securityMeta(r); meta != nil {
		meta.Action = "management.request"
		meta.Success = write
	}
	operator, err := s.server.Service.Operator(ctx, bearer(r), write)
	if err != nil {
		return ctx, r, operator, err
	}
	if operator.AppID == "" {
		ctx = resource.WithManagement(ctx)
	}
	return ctx, r, operator, nil
}
func (s *inventoryService) ListApps(ctx context.Context, in *v1.ListAppsRequest) (*v1.AppPage, error) {
	ctx, _, operator, err := s.authorize(ctx, false)
	if err != nil {
		return nil, err
	}
	var result domain.Page[domain.App]
	if operator.AppID != "" {
		err = s.server.Service.Store.Platform(ctx, false, func(tx ports.PlatformTx) error {
			a, err := tx.App(operator.AppID, false)
			result.Items = []domain.App{a}
			return err
		})
	} else {
		err = s.server.Service.Store.Platform(ctx, false, func(tx ports.PlatformTx) error {
			items, err := tx.Apps(in.GetCursor(), int(in.GetLimit())+1)
			result = page(items, int(in.GetLimit()), func(a domain.App) string { return a.ID })
			return err
		})
	}
	if err != nil {
		return nil, err
	}
	out := new(v1.AppPage)
	err = protojsoncompat.FromDTO(result, out)
	return out, err
}
func (s *inventoryService) CreateApp(ctx context.Context, in *v1.CreateAppRequest) (*v1.App, error) {
	ctx, r, operator, err := s.authorize(ctx, true)
	if err != nil {
		return nil, err
	}
	if operator.AppID != "" {
		return nil, domain.ErrForbidden
	}
	if err, ok := r.Context().Value(bodyDecodeError{}).(error); ok {
		return nil, err
	}
	app, err := s.server.Service.CreateApp(ctx, bearer(r), in.GetBody().GetName(), in.GetBody().GetSlug(), in.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	out := new(v1.App)
	err = protojsoncompat.FromDTO(app, out)
	return out, err
}

// Kratos Router.Handle uses path.Join, which removes a declared trailing slash.
// Resolve explicit Proto aliases before routing without a redirect (a redirect
// could lose the body/method or change the historical response status).
func protoCompatibilityAliases(handler http.Handler, serviceName protoreflect.FullName) http.Handler {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		panic(err)
	}
	service := descriptor.(protoreflect.ServiceDescriptor)
	aliases := map[string]string{}
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
		verb, path := httpRulePath(rule)
		for _, binding := range rule.AdditionalBindings {
			aliasVerb, alias := httpRulePath(binding)
			if aliasVerb == verb {
				aliases[verb+" "+alias] = path
			}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if canonical, ok := aliases[r.Method+" "+r.URL.Path]; ok {
			r = r.Clone(r.Context())
			r.URL.Path = canonical
			r.URL.RawPath = ""
		}
		handler.ServeHTTP(w, r)
	})
}
func httpRulePath(rule *annotations.HttpRule) (string, string) {
	switch p := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		return "GET", p.Get
	case *annotations.HttpRule_Post:
		return "POST", p.Post
	default:
		panic("unsupported compatibility alias HTTP method")
	}
}
