package migrations

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
)

func TestRevisionPersistenceIncludesAtlasProgress(t *testing.T) {
	for _, kind := range []migrate.RevisionType{migrate.RevisionTypeBaseline, migrate.RevisionTypeExecute, migrate.RevisionTypeExecute | migrate.RevisionTypeResolved} {
		want := &migrate.Revision{Version: "001", Description: "fixture", Type: kind, Applied: 2, Total: 3, ExecutedAt: time.Unix(100, 0).UTC(), ExecutionTime: time.Second, Error: "interrupted", ErrorStmt: "SELECT 1", Hash: "verified-file-hash", PartialHashes: []string{"statement-one", "statement-two"}, OperatorVersion: "pinned"}
		data, err := json.Marshal(revisionRecord{&revisionJSON{want, uint(want.Type)}, want.Hash, want.PartialHashes})
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeRevision(string(data))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("revision flags/checksum/progress changed: want %#v, got %#v", want, got)
		}
	}
	if _, err := decodeRevision("{}"); err == nil {
		t.Fatal("empty revision accepted")
	}
}
