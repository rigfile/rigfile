package localui

const pageTemplates = `
{{define "head"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} · Rigfile</title><link rel="stylesheet" href="/style.css"></head><body><main>{{end}}
{{define "foot"}}</main></body></html>{{end}}

{{define "home"}}{{template "head" .}}
<h1>{{.Title}}</h1>
<p class="muted">This page runs on your computer only. It shows what applying the rig would change and what it still needs from you.</p>
{{if .Notice}}<div class="notice">{{.Notice}}</div>{{end}}

<h2>1. What will change</h2>
{{if .PlanErr}}<div class="card bad">{{.PlanErr}}</div>{{else}}<pre>{{.Plan}}</pre>{{end}}

<h2>2. What you need to provide</h2>
{{if not .Needs}}<p class="muted">Nothing: this rig needs no secrets or sign-ins.</p>{{end}}
{{range .Needs}}{{if eq .Kind "secret"}}<div class="card">
<strong>Secret: {{.Ref}}</strong> {{if eq .Status "set"}}<span class="ok">already stored</span>{{else if eq .Status "not set"}}<span class="warn">not set yet</span>{{end}}
{{if .Description}}<div class="muted">{{.Description}}</div>{{end}}
{{if .ObtainURL}}<div class="muted">Get it at {{.ObtainURL}}</div>{{end}}
<form method="post" action="/secret"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ref" value="{{.Ref}}">
<label>Value <input type="password" name="value" autocomplete="off" required></label> <button type="submit">{{if eq .Status "set"}}Replace{{else}}Store{{end}}</button></form>
<div class="muted">Stored in your operating system's secret store. It is never shown again, never sent anywhere, and never put in a config file.</div>
</div>{{else}}<div class="card"><strong>Sign in: {{.Ref}}</strong>{{if .Method}} <span class="muted">({{.Method}})</span>{{end}}
{{if .Description}}<div class="muted">{{.Description}}</div>{{end}}
<div class="muted">Do this yourself in the terminal: <code>rigfile logins</code> walks through every sign-in.</div></div>{{end}}{{end}}

<h2>3. Apply</h2>
<form class="card" method="post" action="/apply"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label><input type="checkbox" name="confirm" value="yes" required> I have read the plan above. Apply it.</label>
<p class="muted">Every file is backed up first, and <code>rigfile rollback</code> undoes it.</p>
<button type="submit">Apply</button></form>

<form method="post" action="/quit"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="quiet" type="submit">Stop this page</button></form>
{{template "foot" .}}{{end}}

{{define "result"}}{{template "head" .}}
<h1>{{if .Failed}}<span class="bad">That did not work</span>{{else}}Done{{end}}</h1>
<pre>{{.Result}}</pre>
<p><a href="/">Back</a></p>
{{template "foot" .}}{{end}}
`
