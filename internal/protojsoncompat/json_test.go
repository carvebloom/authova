package protojsoncompat

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/carvebloom/authova/api/authova/v1"
	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
	"google.golang.org/protobuf/proto"
)

func TestWireCompatibility(t *testing.T) {
	cases := []struct {
		input   string
		message proto.Message
	}{
		{`{"config_version":9223372036854775807,"require_mfa":false}`, new(v1.AppPatch)},
		{`{"config_version":1,"redirect_uris":[]}`, new(v1.ClientPatch)},
		{`{"config_version":1}`, new(v1.ClientPatch)},
		{`{"items":[],"next_cursor":null}`, new(v1.AppPage)},
		{`{"items":[],"next_cursor":""}`, new(v1.AppPage)},
		{`{"items":null,"next_cursor":null}`, new(v1.AppPage)},
	}
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			if err := Unmarshal([]byte(test.input), test.message); err != nil {
				t.Fatal(err)
			}
			encoded, err := Marshal(test.message)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			decoder := func(data []byte, dst *any) {
				t.Helper()
				d := json.NewDecoder(bytes.NewReader(data))
				d.UseNumber()
				if err := d.Decode(dst); err != nil {
					t.Fatal(err)
				}
			}
			decoder([]byte(test.input), &before)
			decoder(encoded, &after)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("wire changed: %s -> %s", test.input, encoded)
			}
		})
	}
}

func TestCaseInsensitiveDuplicateFieldOrder(t *testing.T) {
	for _, input := range []string{`{"Name":"first","name":"last"}`, `{"name":"first","Name":"last"}`} {
		message := new(v1.AppCreate)
		if err := Unmarshal([]byte(input), message); err != nil || message.GetName() != "last" {
			t.Fatal("field order changed", message, err)
		}
	}
}

func TestDuplicateNullAndObjectMergeCompatibility(t *testing.T) {
	cases := []struct {
		input   string
		message proto.Message
		dto     func() any
	}{
		{`{"name":"first","Name":null,"config_version":7,"CONFIG_VERSION":null}`, new(v1.ClientPatch), func() any { return new(application.ClientPatch) }},
		{`{"description":"first","Description":null,"status":"active","Status":null}`, new(v1.AgentPatch), func() any { return new(application.AgentPatch) }},
		{`{"alipay_credentials":{"private_key":"private"},"Alipay_credentials":{"alipay_public_key":"public"}}`, new(v1.ConnectionPatch), func() any { return new(application.ConnectionPatch) }},
		{`{"alipay_credentials":{"private_key":"private"},"Alipay_credentials":null}`, new(v1.ConnectionPatch), func() any { return new(application.ConnectionPatch) }},
		{`{"allowed_scopes":["a"],"Allowed_scopes":["b"]}`, new(v1.ClientPatch), func() any { return new(application.ClientPatch) }},
		{`{"allowed_scopes":["a"],"Allowed_scopes":null}`, new(v1.ClientPatch), func() any { return new(application.ClientPatch) }},
	}
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			before, after := test.dto(), test.dto()
			if err := json.Unmarshal([]byte(test.input), before); err != nil {
				t.Fatal(err)
			}
			if err := Unmarshal([]byte(test.input), test.message); err != nil {
				t.Fatal(err)
			}
			encoded, err := Marshal(test.message)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("legacy decode changed: %#v -> %#v (%s)", before, after, encoded)
			}
		})
	}
}

// Public DTO/schema drift must fail before an HTTP write can commit and then
// discover that its response cannot be represented by the generated contract.
func TestPublicDTOResponseCoverage(t *testing.T) {
	cases := []struct {
		dto     any
		message proto.Message
	}{
		{domain.App{}, new(v1.App)},
		{domain.User{AdminRole: "owner", Email: "masked", Phone: "masked"}, new(v1.User)},
		{domain.Client{}, new(v1.Client)},
		{domain.Session{}, new(v1.Session)},
		{domain.Connection{}, new(v1.Connection)},
		{domain.Operator{}, new(v1.Operator)},
		{domain.Audit{}, new(v1.Audit)},
		{domain.Agent{}, new(v1.Agent)},
		{domain.AgentInstallation{}, new(v1.AgentInstallation)},
		{domain.AgentGrant{}, new(v1.AgentGrant)},
	}
	for _, test := range cases {
		t.Run(string(test.message.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			if err := FromDTO(test.dto, test.message); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(test.dto)
			after, err := Marshal(test.message)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			decode := func(data []byte, out *any) {
				d := json.NewDecoder(bytes.NewReader(data))
				d.UseNumber()
				if err := d.Decode(out); err != nil {
					t.Fatal(err)
				}
			}
			decode(before, &a)
			decode(after, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("response changed: %s -> %s", before, after)
			}
		})
	}
}
func TestRejectUnknownAndProtoIntegerCoercion(t *testing.T) {
	for _, input := range []string{`{"unknown":true}`, `{"config_version":"1"}`, `{"config_version":9223372036854775808}`, `{"config_version":1.2}`, `{} {}`, `{"require_mfa":0}`} {
		if err := Unmarshal([]byte(input), new(v1.AppPatch)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
