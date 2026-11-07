package domain

import (
	"errors"
	"testing"
)

func TestCredentialTransitionsRejectInvalidCombinations(t *testing.T) {
	f := Flow{Purpose: PurposeResetPassword, Step: StepResetPassword, VerifiedValue: "fixture@example.test"}
	if e := f.FinishCredential(PurposeChangeIdentifier, StepBindIdentifier); !errors.Is(e, ErrConflict) {
		t.Fatal("wrong purpose accepted")
	}
	if e := f.FinishCredential(PurposeResetPassword, StepResetPassword); e != nil || !f.Completed || f.Step != StepResetComplete || f.VerifiedValue != "" {
		t.Fatal("completion facts inconsistent", e)
	}
	if e := f.FinishCredential(PurposeResetPassword, StepResetPassword); !errors.Is(e, ErrConflict) {
		t.Fatal("completion replay")
	}
	f = Flow{Purpose: PurposeLogin, Step: StepConsent}
	if e := f.FinishCredential(PurposeLogin, StepConsent); !errors.Is(e, ErrInvalid) || f.Completed {
		t.Fatal("unsupported transition accepted")
	}
}
