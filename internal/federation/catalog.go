package federation

// Types is the supported, ordered configuration vocabulary.
func Types() []string { return []string{"oidc", "github", "wechat", "qq", "alipay"} }

// FixedIssuer keeps social credentials and existing bindings in their original
// identity domain. OIDC uses an administrator-configured discovery issuer.
func FixedIssuer(kind string) (string, bool) {
	switch kind {
	case "", "oidc":
		return "", true
	case "github":
		return "https://github.com", true
	case "wechat":
		return "https://open.weixin.qq.com", true
	case "qq":
		return "https://graph.qq.com", true
	case "alipay":
		return "https://openauth.alipay.com", true
	default:
		return "", false
	}
}
