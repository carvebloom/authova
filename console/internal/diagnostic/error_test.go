package diagnostic

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

type associatedFailure struct{ id string }

func (*associatedFailure) Error() string                 { panic("must not format dependency error") }
func (e *associatedFailure) DiagnosticRequestID() string { return e.id }

func TestWrappedSDKAssociationSurvivesLostResponseBody(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	err := Wrap(&associatedFailure{id: "upstream-123"}, "console", "upstream")
	Log(WithRequest(context.Background(), "console-123"), "console", "upstream", err)
	for _, want := range []string{"console-123", "upstream-123", "first_application_contact", "frames"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatal("lost local/upstream association or useful diagnostic")
		}
	}
	if Inspect(&associatedFailure{id: "unsafe ID\tCANARY"}).UpstreamID != "" {
		t.Fatal("untrusted association escaped bounds")
	}
}
