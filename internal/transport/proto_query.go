package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/api/authova/wire"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/protojsoncompat"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type queryDecodeError struct{}

// Decode errors are deferred until after the original App/header admission
// order. Binding malformed parameters must not turn a prior 401 into a 400.
func decodeProtoQuery(r *http.Request, in any) error {
	switch request := in.(type) {
	case *v1.ListAppsRequest:
		cursor, limit := paging(r)
		request.Cursor = proto.String(cursor)
		request.Limit = proto.Int64(int64(limit))
		return nil
	case *v1.CreateAppRequest:
		request.IdempotencyKey = proto.String(r.Header.Get("Idempotency-Key"))
		return nil
	}
	message := in.(proto.Message).ProtoReflect()
	if managementInputs[message.Descriptor().FullName()] {
		// Management historically ignores unknown query keys and uses bounded
		// default pagination; query capabilities intentionally use strict binding.
		cursor, limit := paging(r)
		for i := 0; i < message.Descriptor().Fields().Len(); i++ {
			field := message.Descriptor().Fields().Get(i)
			if !proto.HasExtension(field.Options(), wire.E_Parameter) {
				continue
			}
			meta := parameterMeta(field)
			if meta["in"] == "header" {
				message.Set(field, protoreflect.ValueOfString(r.Header.Get(meta["name"].(string))))
			} else if meta["in"] == "query" {
				switch field.Name() {
				case "limit":
					message.Set(field, protoreflect.ValueOfInt64(int64(limit)))
				case "cursor":
					message.Set(field, protoreflect.ValueOfString(cursor))
				default:
					message.Set(field, protoreflect.ValueOfString(r.URL.Query().Get(meta["name"].(string))))
				}
			}
		}
		return nil
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err == nil {
		for key, vs := range values {
			var f protoreflect.FieldDescriptor
			for i := 0; i < message.Descriptor().Fields().Len(); i++ {
				candidate := message.Descriptor().Fields().Get(i)
				if candidate.JSONName() != key || !proto.HasExtension(candidate.Options(), wire.E_Parameter) {
					continue
				}
				meta := parameterMeta(candidate)
				if meta["in"] == "query" {
					f = candidate
					break
				}
			}
			if f == nil || len(vs) != 1 {
				err = domain.ErrInvalid
				break
			}
			if f.Kind() == protoreflect.Int64Kind {
				n, e := strconv.ParseInt(vs[0], 10, 64)
				if e != nil || n < 1 || n > 100 {
					err = domain.ErrInvalid
					break
				}
				message.Set(f, protoreflect.ValueOfInt64(n))
			} else {
				message.Set(f, protoreflect.ValueOfString(vs[0]))
			}
		}
	}
	if err != nil {
		*r = *r.WithContext(context.WithValue(r.Context(), queryDecodeError{}, domain.ErrInvalid))
	}
	return nil
}

var managementInputs = func() map[protoreflect.FullName]bool {
	inputs := map[protoreflect.FullName]bool{}
	methods := v1.File_api_authova_v1_management_proto.Services().ByName("Management").Methods()
	for i := 0; i < methods.Len(); i++ {
		inputs[methods.Get(i).Input().FullName()] = true
	}
	return inputs
}()

func parameterMeta(f protoreflect.FieldDescriptor) map[string]any {
	var meta map[string]any
	_ = json.Unmarshal([]byte(proto.GetExtension(f.Options(), wire.E_Parameter).(string)), &meta)
	return meta
}
func (s *Server) protoQueryRouter(fallback http.Handler) http.Handler {
	h := khttp.NewServer(khttp.StrictSlash(false), khttp.Timeout(0), khttp.RequestQueryDecoder(decodeProtoQuery), khttp.RequestDecoder(decodeProtoBody), khttp.ErrorEncoder(func(w http.ResponseWriter, r *http.Request, err error) {
		if !errors.Is(err, errResponseWritten) {
			problem(w, r, err)
		}
	}), khttp.ResponseEncoder(func(w http.ResponseWriter, r *http.Request, value any) error {
		status := protoSuccessStatus(r.Context())
		if status == http.StatusNoContent {
			// Kratos's encoder writer buffers WriteHeader until Write. A 204
			// has no body to flush it, so commit the status on the transport's
			// underlying writer after the service has confirmed the DB commit.
			raw, ok := khttp.ResponseWriterFromServerContext(r.Context())
			if !ok {
				return domain.ErrUnavailable
			}
			raw.WriteHeader(status)
			return nil
		}
		data, err := protojsoncompat.Marshal(value.(proto.Message))
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, err = w.Write(append(data, '\n'))
		return err
	}))
	v1.RegisterQueryHTTPServer(h, &queryService{server: s})
	v1.RegisterAppInventoryHTTPServer(h, &inventoryService{server: s})
	v1.RegisterManagementHTTPServer(h, &managementService{server: s})
	h.HandlePrefix("/", fallback)
	return managementSlashCompatibility(protoCompatibilityAliases(h, "authova.management.v1.AppInventory"))
}

type queryService struct{ server *Server }

var _ v1.QueryHTTPServer = (*queryService)(nil)

func (q *queryService) read(ctx context.Context, in proto.Message, kind string, out proto.Message) error {
	r, ok := khttp.RequestFromServerContext(ctx)
	if !ok {
		return domain.ErrUnavailable
	}
	w, ok := khttp.ResponseWriterFromServerContext(ctx)
	if !ok {
		return domain.ErrUnavailable
	}
	m := in.ProtoReflect()
	stringField := func(name string) string {
		f := m.Descriptor().Fields().ByName(protoreflect.Name(name))
		if f == nil {
			return ""
		}
		return m.Get(f).String()
	}
	value, err := q.server.queryResult(w, r, stringField("app_id"), func() (application.QueryRequest, error) {
		input := application.QueryRequest{Kind: kind, ID: stringField("id"), UserID: stringField("user_id"), Cursor: stringField("cursor"), AgentID: stringField("agent_id"), InstallationID: stringField("installation_id"), TenantID: stringField("tenant_id"), Limit: 20}
		if f := m.Descriptor().Fields().ByName("limit"); f != nil && m.Has(f) {
			input.Limit = int(m.Get(f).Int())
		}
		if err, ok := r.Context().Value(queryDecodeError{}).(error); ok {
			return input, err
		}
		return input, nil
	})
	if err != nil {
		return err
	}
	if err = protojsoncompat.FromDTO(value, out); err != nil {
		return fmt.Errorf("query projection %s: %w", kind, err)
	}
	return nil
}

func (q *queryService) QueryQueryPrincipal(ctx context.Context, in *v1.QueryQueryPrincipalRequest) (*v1.QueryPrincipal, error) {
	out := new(v1.QueryPrincipal)
	err := q.read(ctx, in, "context", out)
	return out, err
}

func (q *queryService) QueryQueryConfiguration(ctx context.Context, in *v1.QueryQueryConfigurationRequest) (*v1.QueryConfiguration, error) {
	out := new(v1.QueryConfiguration)
	err := q.read(ctx, in, "configuration", out)
	return out, err
}

func (q *queryService) QueryQueryUserPage(ctx context.Context, in *v1.QueryQueryUserPageRequest) (*v1.QueryUserPage, error) {
	out := new(v1.QueryUserPage)
	err := q.read(ctx, in, "users", out)
	return out, err
}

func (q *queryService) QueryQueryUser(ctx context.Context, in *v1.QueryQueryUserRequest) (*v1.QueryUser, error) {
	out := new(v1.QueryUser)
	err := q.read(ctx, in, "user", out)
	return out, err
}

func (q *queryService) QuerySessionPage(ctx context.Context, in *v1.QuerySessionPageRequest) (*v1.QueryContractSessionPage, error) {
	out := new(v1.QueryContractSessionPage)
	err := q.read(ctx, in, "sessions", out)
	return out, err
}

func (q *queryService) QueryAgentPage(ctx context.Context, in *v1.QueryAgentPageRequest) (*v1.QueryContractAgentPage, error) {
	out := new(v1.QueryContractAgentPage)
	err := q.read(ctx, in, "agents", out)
	return out, err
}

func (q *queryService) QueryAgent(ctx context.Context, in *v1.QueryAgentRequest) (*v1.Agent, error) {
	out := new(v1.Agent)
	err := q.read(ctx, in, "agent", out)
	return out, err
}

func (q *queryService) QueryAgentInstallationPage(ctx context.Context, in *v1.QueryAgentInstallationPageRequest) (*v1.QueryContractAgentInstallationPage, error) {
	out := new(v1.QueryContractAgentInstallationPage)
	err := q.read(ctx, in, "installations", out)
	return out, err
}

func (q *queryService) QueryAgentInstallation(ctx context.Context, in *v1.QueryAgentInstallationRequest) (*v1.AgentInstallation, error) {
	out := new(v1.AgentInstallation)
	err := q.read(ctx, in, "installation", out)
	return out, err
}

func (q *queryService) QueryAgentGrantPage(ctx context.Context, in *v1.QueryAgentGrantPageRequest) (*v1.QueryContractAgentGrantPage, error) {
	out := new(v1.QueryContractAgentGrantPage)
	err := q.read(ctx, in, "grants", out)
	return out, err
}

func (q *queryService) QueryAgentGrant(ctx context.Context, in *v1.QueryAgentGrantRequest) (*v1.AgentGrant, error) {
	out := new(v1.AgentGrant)
	err := q.read(ctx, in, "grant", out)
	return out, err
}

func (q *queryService) QueryQueryAuditPage(ctx context.Context, in *v1.QueryQueryAuditPageRequest) (*v1.QueryAuditPage, error) {
	out := new(v1.QueryAuditPage)
	err := q.read(ctx, in, "audit", out)
	return out, err
}
