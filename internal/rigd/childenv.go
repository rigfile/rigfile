package rigd

// CAVars are the variables that make common runtimes trust the session CA. The CA is given to the launched child only,
// never installed in an OS trust store.
var CAVars = []string{"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"}

// ChildEnv is what a Level 2 child gets in place of its real secrets: the surrogates, the proxy with the session's
// credentials, and the CA. caPath is the file holding reply.CAPEM.
func ChildEnv(reply *OpenReply, caPath string) map[string]string {
	env := map[string]string{}
	for k, v := range reply.Surrogates {
		env[k] = v
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		env[k] = reply.ProxyURL
	}
	env["NO_PROXY"], env["no_proxy"] = "", ""
	env["NODE_USE_ENV_PROXY"] = "1" // Node's built-in fetch only follows the proxy variables when asked
	for _, k := range CAVars {
		env[k] = caPath
	}
	return env
}
