package analyze

import (
	"regexp"
	"strings"
)

// ruleInfo gives every rule id its level and its fixed message. Messages never include file content.
var ruleInfo = map[string]ruleDef{
	"net.http":               {Notice, "makes network requests"},
	"net.exfil-host":         {Caution, "sends data to a raw IP address, a paste/webhook/tunnel service or a URL shortener"},
	"net.raw-socket":         {Caution, "opens a raw network connection"},
	"exec.download-run":      {Danger, "downloads code and runs it without saving or checking it"},
	"obf.base64-exec":        {Danger, "decodes hidden content and executes it"},
	"obf.eval":               {Caution, "evaluates a string as code"},
	"obf.blob":               {Caution, "contains a long encoded blob or character-by-character string assembly"},
	"cred.access":            {Caution, "reads credential files, key stores or browser profiles"},
	"cred.env-dump":          {Caution, "dumps the whole environment, which may hold secrets"},
	"combo.credential-exfil": {Danger, "reads credentials and makes network requests in the same file"},
	"persist.schedule":       {Caution, "installs a scheduled task, launch agent or service"},
	"persist.shell-rc":       {Danger, "edits shell startup files"},
	"persist.git-hooks":      {Danger, "changes git hooks or bypasses them"},
	"persist.ai-config":      {Caution, "changes the AI tools' own configuration or credentials"},
	"priv.escalate":          {Caution, "asks for administrator rights or opens file permissions wide"},
	"fs.destructive":         {Danger, "deletes or overwrites broad parts of the file system"},
	"pkg.install-remote":     {Caution, "installs a package straight from a URL or repository"},
	"ai.bypass-permissions":  {Caution, "turns off permission prompts or sandboxing of an AI tool"},
	"bin.executable":         {Caution, "contains a compiled executable"},
	"bin.opaque":             {Notice, "contains a binary file"},
	"struct.mcp-http":        {Caution, "an MCP server is reached over plain http on a non-local address"},
	"struct.mcp-shell":       {Caution, "an MCP server or hook runs through a shell command line"},
	"struct.mcp-download":    {Danger, "an MCP server or hook downloads and runs code"},
	"struct.allow-broad":     {Caution, "grants blanket permission to run any command"},
	"struct.hook-script":     {Notice, "installs a hook that runs a script on the user's machine"},
	"struct.mcp-launcher":    {Notice, "an MCP server runs a program fetched by a package launcher"},
	"inj.ignore-previous":    {Caution, "tells the AI to ignore its earlier instructions"},
	"inj.conceal":            {Caution, "tells the AI to hide something from the user"},
	"inj.exfil":              {Caution, "tells the AI to send credentials or private files somewhere"},
	"inj.disable-safety":     {Caution, "tells the AI to disable a safety check, hook or permission"},
	"hidden.bidi":            {Danger, "contains text-direction override characters that can disguise what the text says"},
	"hidden.tag":             {Danger, "contains invisible tag characters that can carry hidden instructions"},
	"hidden.zero-width":      {Caution, "contains zero-width characters"},
	"hidden.control":         {Caution, "contains unusual control characters"},
	"hidden.html-comment":    {Caution, "contains an HTML comment that addresses the AI"},
}

func re(p string) *regexp.Regexp { return regexp.MustCompile(p) }

// ---- hidden characters (all text) ---------------------------------------------------------------------------------

var hiddenRules = []rule{
	{id: "hidden.bidi", re: re("[\u202A-\u202E\u2066-\u2069]")},
	{id: "hidden.tag", re: re("[\U000E0000-\U000E007F]")},
	{id: "hidden.zero-width", re: re("[\u200B\u200C\u200D\u2060]|.\uFEFF")},
	{id: "hidden.control", re: re("[\x01-\x08\x0B\x0C\x0E-\x1F\x7F]")},
}

// ---- instruction text ---------------------------------------------------------------------------------------------

var instructionRules = []rule{
	{id: "inj.ignore-previous", neg: true, re: re(`(?i)\b(ignore|disregard|forget)\b[^.\n]{0,20}\b(all |any |the |your |every )?(previous|prior|above|earlier|preceding|system)\b[^.\n]{0,20}\b(instructions?|rules?|prompts?|guidelines?|messages?)\b`)},
	{id: "inj.ignore-previous", neg: true, re: re(`(?i)\byou are now\b[^.\n]{0,30}\b(unrestricted|jailbroken|developer mode|dan)\b`)},
	{id: "inj.conceal", neg: false, re: re(`(?i)\b(do not|don'?t|never)\s+(tell|inform|notify|alert|warn)\s+the\s+user\b`)},
	{id: "inj.conceal", neg: true, re: re(`(?i)\bhide\b[^.\n]{0,30}\bfrom\s+the\s+user\b`)},
	{id: "inj.conceal", neg: true, re: re(`(?i)\bwithout\s+(telling|informing|asking|notifying)\s+the\s+user\b`)},
	{id: "inj.exfil", neg: true, re: re(`(?i)\b(send|upload|post|email|exfiltrate|transmit|forward)\b[^.\n]{0,60}(\.ssh|id_rsa|id_ed25519|\.env\b|credentials|api[ _-]?keys?|secret|token|password|private key)`)},
	{id: "inj.exfil", neg: true, re: re(`(?i)\b(read|cat|print|output|include)\b[^.\n]{0,30}(~/\.ssh|id_rsa|id_ed25519|\.aws/credentials|\.env\b)[^.\n]{0,60}\b(and|then)\b[^.\n]{0,20}\b(send|post|upload|include|paste)\b`)},
	{id: "inj.disable-safety", neg: true, re: re(`(?i)\b(disable|turn off|bypass|skip|circumvent|remove)\b[^.\n]{0,30}\b(safety|guard ?rails?|hooks?|permissions?|sandbox|deny rules?|secret scan(ning)?|pre-commit|security (checks?|controls?))\b`)},
	{id: "hidden.html-comment", re: re(`(?is)<!--.{0,400}\b(ignore|assistant|claude|codex|gemini|system prompt|instruction|do not tell|you must)\b.{0,400}-->`)},
}

// ---- scripts ------------------------------------------------------------------------------------------------------

var (
	exfilHost = re(`(?i)https?://(\d{1,3}\.){3}\d{1,3}|pastebin\.com|paste\.ee|hastebin|transfer\.sh|webhook\.site|requestbin|pipedream\.net|ngrok(-free)?\.(io|app)|trycloudflare\.com|discord(app)?\.com/api/webhooks|api\.telegram\.org/bot|\bix\.io\b|termbin\.com|bit\.ly|tinyurl\.com|t\.co/|is\.gd|serveo\.net|localtunnel\.me`)

	shellNet = re(`(?i)(^|[\s;&|(` + "`" + `$])(curl|wget|httpie|aria2c|lwp-download)\s`)
	psNet    = re(`(?i)\b(Invoke-WebRequest|Invoke-RestMethod|Net\.WebClient|Start-BitsTransfer|DownloadString|DownloadFile|iwr|irm)\b`)
	pyNet    = re(`(?i)\b(requests\.(get|post|put)|urllib\.request|urlopen|http\.client|httpx\.|aiohttp|socket\.socket|smtplib)\b`)
	jsNet    = re(`(?i)\b(fetch\(|XMLHttpRequest|axios\.|https?\.request|https?\.get\(|net\.connect|WebSocket\()`)
	rawSock  = re(`(?i)(/dev/(tcp|udp)/|\b(nc|ncat|netcat)\b\s+(-\w+\s+)*-(e|c)\b|\b(nc|ncat|netcat|socat)\b[^\n]*\s[\w.\-]+\s+\d{2,5}\b|socket\.socket\(|net\.connect\()`)

	downloadRun = []*regexp.Regexp{
		re(`(?i)\b(curl|wget)\b[^|;&\n]*\|\s*(sudo\s+(-\w+\s+)*)?(ba|z|da|k|fi)?sh\b`),
		re(`(?i)\b(curl|wget)\b[^|;&\n]*\|\s*(sudo\s+)?(python3?|perl|ruby|node)\b`),
		re(`(?i)\b(ba|z|da|k)?sh\b\s+(-c\s+)?["']?\$?\(?\s*<?\(?\s*(curl|wget)\b`),
		re(`(?i)\beval\b[^\n;]*\$\(\s*(curl|wget)\b`),
		re(`(?i)\bsource\s+<\(\s*(curl|wget)\b`),
		re(`(?i)\b(iwr|irm|Invoke-WebRequest|Invoke-RestMethod)\b[^|;\n]*\|\s*(iex|Invoke-Expression)\b`),
		re(`(?i)\b(iex|Invoke-Expression)\b\s*\(?\s*\(?\s*(New-Object\s+(System\.)?Net\.WebClient|Invoke-WebRequest|iwr|irm)\b`),
		re(`(?i)\bpython3?\s+-c\s+["']?\$\(\s*(curl|wget)\b`),
		re(`(?i)\bcertutil\b[^\n]*-urlcache\b`),
		re(`(?i)\bmshta\b\s+https?://`),
	}

	base64Exec = []*regexp.Regexp{
		re(`(?i)\bbase64\s+(-d|-D|--decode)\b[^|;\n]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b`),
		re(`(?i)\bbase64\s+(-d|-D|--decode)\b[^\n]*\|\s*(python3?|perl|ruby|node|eval)\b`),
		re(`(?i)\beval\b[^\n]*\$\(\s*(echo|printf)\b[^\n]*\|\s*base64\s+(-d|-D|--decode)`),
		re(`(?i)(-EncodedCommand|-enc|-ec)\s+[A-Za-z0-9+/=]{20,}`),
		re(`(?i)\b(iex|Invoke-Expression)\b[^\n]*FromBase64String`),
		re(`(?i)FromBase64String[^\n]*\b(iex|Invoke-Expression)\b`),
		re(`(?i)\b(exec|eval)\s*\([^\n]*(b64decode|decodebytes|base64\.)`),
		re(`(?i)\b(exec|eval)\s*\(\s*(zlib|gzip|bz2|lzma)\.decompress`),
		re(`(?i)\beval\s*\(\s*atob\s*\(|\bnew\s+Function\s*\(\s*atob\s*\(|\beval\s*\(\s*Buffer\.from\([^\n]*base64`),
		re(`(?i)\bmarshal\.loads\b[^\n]*\bexec\b`),
	}

	evalRe = map[kind]*regexp.Regexp{
		kShell:  re(`(^|[\s;&|(])eval\s`),
		kPython: re(`\b(eval|exec|compile)\s*\(`),
		kJS:     re(`\beval\s*\(|\bnew\s+Function\s*\(|\bvm\.runIn`),
	}

	blobRe = []*regexp.Regexp{
		re(`[A-Za-z0-9+/]{200,}={0,2}`),
		re(`(\\x[0-9a-fA-F]{2}){16,}`),
		re(`\b[0-9a-fA-F]{160,}\b`),
		re(`(?i)(chr\(\s*\d+\s*\)\s*\+\s*){5,}`),
		re(`(?i)String\.fromCharCode\(\s*(\d+\s*,\s*){8,}`),
		re(`(?i)\[char\]\s*\d+(\s*\+\s*\[char\]\s*\d+){5,}`),
	}

	credPath = re(`(?i)(~|\$HOME|\$\{HOME\}|%USERPROFILE%|\$env:USERPROFILE|\$env:HOME|expanduser\(['"]~['"]\))?[/\\]?\.(ssh|aws|gnupg|kube|azure)\b|\.config[/\\]gcloud|\.netrc\b|\.npmrc\b|\.pypirc\b|\.git-credentials|\.docker[/\\]config\.json|\bid_(rsa|ed25519|ecdsa|dsa)\b|Library/Keychains|login\.keychain|Login Data\b|Cookies\.sqlite|Local State\b|Application Support/(Google/Chrome|Firefox|BraveSoftware)|AppData[/\\](Local|Roaming)[/\\](Google|Mozilla|BraveSoftware|Microsoft[/\\]Credentials)|\.claude[/\\]\.credentials\.json|\.claude\.json\b|\.codex[/\\]auth\.json|\.config[/\\]gh[/\\]hosts\.yml|/etc/(shadow|sudoers)`)

	envDump = re(`(?i)(^|[\s;&|(])(printenv|env)\s*($|[|>;&)])|\bexport\s+-p\b|\bGet-ChildItem\s+env:|\b(gci|dir|ls)\s+env:|\[Environment\]::GetEnvironmentVariables|\bos\.environ\b\s*($|[),\]]|\.copy\(|\.items\(|\.keys\(|\.values\()|dict\(\s*os\.environ|json\.dumps\(\s*(dict\()?os\.environ|Object\.(keys|entries|values)\(\s*process\.env|JSON\.stringify\(\s*process\.env|\bset\s*\|`)

	schedule = re(`(?i)\bcrontab\b|/etc/cron|\blaunchctl\s+(load|bootstrap|submit)|Launch(Agents|Daemons)|\bsystemctl\s+(--user\s+)?(enable|start|link)|/etc/systemd|\bschtasks\b|New-ScheduledTask|Register-ScheduledTask|CurrentVersion\\Run|\breg\s+add\b[^\n]*\\Run\b|\bat\s+now\b|\bWinlogon\b`)
	shellRC  = re(`(?i)(>>|>|tee\s+(-a\s+)?|Add-Content\b[^\n]*|Set-Content\b[^\n]*|Out-File\b[^\n]*)\s*["']?(~|\$HOME|\$\{HOME\}|\$env:USERPROFILE)?[/\\]?\.?(bashrc|zshrc|zprofile|zshenv|bash_profile|bash_login|profile|config[/\\]fish[/\\]config\.fish)\b|\$PROFILE\b[^\n]*(Add-Content|Set-Content|Out-File)|(Add-Content|Set-Content|Out-File)\b[^\n]*\$PROFILE\b`)
	gitHooks = re(`(?i)\.git[/\\]hooks\b|core\.hooksPath|--no-verify\b|git\s+config\b[^\n]*\bhooksPath\b|HUSKY\s*=\s*0|SKIP_GIT_HOOKS`)
	aiConfig = re(`(?i)[~/\\]\.claude[/\\](settings(\.local)?\.json|CLAUDE\.md|hooks|agents|skills|commands)|~?[/\\]\.codex[/\\](config\.toml|AGENTS\.md)|[~/\\]\.gemini[/\\]settings\.json|[~/\\]\.cursor[/\\]mcp\.json|\.gitconfig\b|\.config[/\\]git[/\\](config|ignore)`)

	privRe    = re(`(?i)(^|[\s;&|(])sudo\s|chmod\s+(-R\s+)?(a\+rwx|0?777|o\+w|a\+w)\b|Start-Process\b[^\n]*-Verb\s+RunAs|chown\s+-R\s+root|\bsetuid\b|\bicacls\b[^\n]*Everyone`)
	destroy   = re(`(?i)\brm\s+(-[a-zA-Z]*\s+)*-[a-zA-Z]*[rR][a-zA-Z]*\s+(-[a-zA-Z-]+\s+)*(--no-preserve-root\s+)?["']?(/|~|\$HOME|\$\{HOME\}|/\*|~/\*|\$HOME/\*|\$\{HOME\}/\*|\*)["']?(\s|$|;)|\brm\s+-[a-zA-Z]*[rR][a-zA-Z]*\s+-[a-zA-Z]*f\S*\s+["']?(/|~|\$HOME)|Remove-Item\b[^\n]*-Recurse[^\n]*(\$HOME|~|\$env:USERPROFILE|C:\\(Users|Windows)?\\?\*?)|\bdd\s+if=\S+\s+of=/dev/(sd|nvme|disk|hd)|\bmkfs(\.\w+)?\s|\bshred\b[^\n]*(/dev/|~)|:\(\)\{\s*:\|:&\s*\};:|\bformat\s+[a-zA-Z]:`)
	pkgRemote = re(`(?i)\bpip3?\s+install\b[^\n]*(git\+|https?://|--index-url|--extra-index-url)|\bnpm\s+(i|install|add)\b[^\n]*(https?://|git\+|github:)|\bgo\s+install\b[^\n]*@(master|main|latest)|\bcargo\s+install\b[^\n]*--git\b|\bgem\s+install\b[^\n]*--source`)
	bypass    = re(`(?i)--dangerously-skip-permissions|dangerouslyDisableSandbox|["']?bypassPermissions["']?|--yolo\b|approval_policy\s*[=:]\s*["']?never|sandbox_mode\s*[=:]\s*["']?danger-full-access|--dangerously-bypass-approvals-and-sandbox|allowUnsandboxedCommands["']?\s*[:=]\s*true|disableBypassPermissionsMode["']?\s*[:=]\s*["']?(false|enable)`)
)

func stripComment(k kind, line string) string {
	t := strings.TrimSpace(line)
	switch k {
	case kShell, kPython:
		if strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#!") {
			return ""
		}
	case kPowerShell:
		if strings.HasPrefix(t, "#") {
			return ""
		}
	case kJS:
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
			return ""
		}
	case kBatch:
		if strings.HasPrefix(strings.ToLower(t), "rem ") || strings.HasPrefix(t, "::") {
			return ""
		}
	}
	return line
}

func scriptHits(k kind, data []byte) []hit {
	var out []hit
	lines := strings.Split(string(data), "\n")
	add := func(rule string, i int) { out = append(out, hit{rule: rule, line: i + 1}) }
	matchAny := func(res []*regexp.Regexp, ln string) bool {
		for _, r := range res {
			if r.MatchString(ln) {
				return true
			}
		}
		return false
	}
	for i, raw := range lines {
		ln := stripComment(k, strings.TrimRight(raw, "\r"))
		if strings.TrimSpace(ln) == "" {
			continue
		}
		// network
		netMatch := false
		switch k {
		case kShell:
			netMatch = shellNet.MatchString(ln)
		case kPowerShell:
			netMatch = psNet.MatchString(ln) || shellNet.MatchString(ln)
		case kPython:
			netMatch = pyNet.MatchString(ln) || shellNet.MatchString(ln) && strings.Contains(ln, "subprocess")
		case kJS:
			netMatch = jsNet.MatchString(ln) || shellNet.MatchString(ln) && strings.Contains(ln, "exec")
		case kBatch:
			netMatch = shellNet.MatchString(ln) || psNet.MatchString(ln) || strings.Contains(strings.ToLower(ln), "bitsadmin")
		}
		if netMatch {
			add("net.http", i)
		}
		if exfilHost.MatchString(ln) && (netMatch || rawSock.MatchString(ln)) {
			add("net.exfil-host", i)
		}
		if rawSock.MatchString(ln) {
			add("net.raw-socket", i)
		}
		if matchAny(downloadRun, ln) {
			add("exec.download-run", i)
		}
		if matchAny(base64Exec, ln) {
			add("obf.base64-exec", i)
		}
		if r, ok := evalRe[k]; ok && r.MatchString(ln) {
			add("obf.eval", i)
		}
		if matchAny(blobRe, ln) {
			add("obf.blob", i)
		}
		if credPath.MatchString(ln) {
			add("cred.access", i)
		}
		if envDump.MatchString(ln) {
			add("cred.env-dump", i)
		}
		if schedule.MatchString(ln) {
			add("persist.schedule", i)
		}
		if shellRC.MatchString(ln) {
			add("persist.shell-rc", i)
		}
		if gitHooks.MatchString(ln) {
			add("persist.git-hooks", i)
		}
		if aiConfig.MatchString(ln) && !credPath.MatchString(ln) {
			add("persist.ai-config", i)
		}
		if privRe.MatchString(ln) {
			add("priv.escalate", i)
		}
		if destroy.MatchString(ln) {
			add("fs.destructive", i)
		}
		if pkgRemote.MatchString(ln) {
			add("pkg.install-remote", i)
		}
		if bypass.MatchString(ln) {
			add("ai.bypass-permissions", i)
		}
	}
	return out
}
