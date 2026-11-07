package domain

// FlowStep and FlowPurpose describe a finite protocol, not a configurable DSL.
// Credential transitions below keep the completed flag and final step atomic.
type FlowStep string
type FlowPurpose string

const (
	StepChoose                 FlowStep    = "choose_method"
	StepPassword               FlowStep    = "password"
	StepVerifyIdentifier       FlowStep    = "verify_identifier"
	StepSetPassword            FlowStep    = "set_password"
	StepResetPassword          FlowStep    = "reset_password"
	StepBindIdentifier         FlowStep    = "bind_identifier"
	StepResetComplete          FlowStep    = "reset_complete"
	StepAccountComplete        FlowStep    = "account_complete"
	StepMFA                    FlowStep    = "mfa"
	StepMFAReplaceConfirm      FlowStep    = "mfa_replace_confirm"
	StepMFAMaintenanceComplete FlowStep    = "mfa_maintenance_complete"
	StepConsent                FlowStep    = "consent"
	StepComplete               FlowStep    = "complete"
	StepFederation             FlowStep    = "federation"
	StepCancelled              FlowStep    = "cancelled"
	PurposeAuthorize           FlowPurpose = "authorize"
	PurposeRegister            FlowPurpose = "register"
	PurposeLogin               FlowPurpose = "login"
	PurposeResetPassword       FlowPurpose = "reset_password"
	PurposeChangeIdentifier    FlowPurpose = "change_identifier"
	PurposeLink                FlowPurpose = "link"
	PurposeMFAMaintenance      FlowPurpose = "mfa_maintenance"
)

func (f *Flow) FinishCredential(purpose FlowPurpose, from FlowStep) error {
	if f.Completed || f.Purpose != purpose || f.Step != from {
		return ErrConflict
	}
	switch {
	case purpose == PurposeResetPassword && from == StepResetPassword:
		f.Step = StepResetComplete
	case purpose == PurposeChangeIdentifier && from == StepBindIdentifier:
		f.Step = StepAccountComplete
	default:
		return ErrInvalid
	}
	f.Completed = true
	f.VerifiedValue = ""
	return nil
}
