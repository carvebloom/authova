package domain

// ResourceLimits bound one App's use of a shared deployment. Concurrency is per
// process; request and challenge counters use the shared deployment limiter.
type ResourceLimits struct {
	HTTPConcurrency        int `json:"http_concurrency"`
	KDFConcurrency         int `json:"kdf_concurrency"`
	DBConcurrency          int `json:"db_concurrency"`
	RequestsPerSecond      int `json:"requests_per_second"`
	EmailChallengesPerHour int `json:"email_challenges_per_hour"`
	SMSChallengesPerHour   int `json:"sms_challenges_per_hour"`
}

func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{HTTPConcurrency: 256, KDFConcurrency: 2, DBConcurrency: 32, RequestsPerSecond: 20000, EmailChallengesPerHour: 100000, SMSChallengesPerHour: 10000}
}

func (l ResourceLimits) Valid() bool {
	return l.HTTPConcurrency >= 1 && l.HTTPConcurrency <= 100000 && l.KDFConcurrency >= 1 && l.KDFConcurrency <= 512 && l.DBConcurrency >= 1 && l.DBConcurrency <= 4096 && l.RequestsPerSecond >= 1 && l.RequestsPerSecond <= 1000000 && l.EmailChallengesPerHour >= 1 && l.EmailChallengesPerHour <= 10000000 && l.SMSChallengesPerHour >= 1 && l.SMSChallengesPerHour <= 10000000
}

func (a App) ResourceLimits() ResourceLimits {
	if a.Resources == nil {
		return DefaultResourceLimits()
	}
	return *a.Resources
}
