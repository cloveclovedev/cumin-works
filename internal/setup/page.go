package setup

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"sync"
)

// localPage is the local web page of the Manifest flow. "/start" sends the
// manifest of the expected App to GitHub with a form. GitHub sends the browser
// back to "/callback" with a code.
//
// The page never logs a request: the address of the callback holds the code.
type localPage struct {
	gitHubURL string
	org       string
	prefix    string
	baseURL   string
	callbacks chan<- callback

	mu    sync.Mutex
	app   string // the App that the flow waits for; empty when it waits for none
	state string
}

// expect sets the App and the state that the flow waits for.
func (p *localPage) expect(app, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.app, p.state = app, state
}

func (p *localPage) expected() (app, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.app, p.state
}

// take checks the state of a callback and uses the expectation up in one step.
// So a second callback with the same state (a reload of the tab) finds no
// expectation, and it cannot wait in line for the next App.
func (p *localPage) take(state string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.app == "" || state == "" || state != p.state {
		return false
	}
	p.app, p.state = "", ""
	return true
}

func (p *localPage) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /start", p.start)
	mux.HandleFunc("GET /callback", p.callback)
	return mux
}

var startTemplate = template.Must(template.New("start").Parse(`<!doctype html>
<meta charset="utf-8">
<title>cumin setup</title>
<h1>Register the GitHub App {{.Name}}</h1>
<p>This page sends the manifest below to GitHub. On GitHub, select "Create GitHub App".</p>
<pre>{{.Pretty}}</pre>
<form action="{{.Action}}" method="post">
  <input type="hidden" name="manifest" value="{{.Manifest}}">
  <button type="submit">Send the manifest to GitHub</button>
</form>
<script>document.forms[0].submit()</script>
`))

func (p *localPage) start(w http.ResponseWriter, r *http.Request) {
	app, state := p.expected()
	if app == "" || r.URL.Query().Get("app") != app {
		http.Error(w, "cumin setup does not wait for this App now.", http.StatusConflict)
		return
	}
	manifest, err := BuildManifest(p.org, p.prefix, app, p.baseURL+"/callback")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	compact, _ := json.Marshal(manifest)
	pretty, _ := json.MarshalIndent(manifest, "", "  ")
	action := p.gitHubURL + "/organizations/" + url.PathEscape(p.org) + "/settings/apps/new?state=" + url.QueryEscape(state)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = startTemplate.Execute(w, map[string]string{
		"Name":     manifest.Name,
		"Pretty":   string(pretty),
		"Manifest": string(compact),
		"Action":   action,
	})
}

func (p *localPage) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	code, state := query.Get("code"), query.Get("state")
	// A callback with a wrong state is refused here and does not stop the
	// flow. Otherwise any local page could end the setup with one request.
	if code == "" || !p.take(state) {
		http.Error(w, "This callback does not belong to the App that cumin setup waits for now. Nothing is stored.", http.StatusBadRequest)
		return
	}
	select {
	case p.callbacks <- callback{code: code}:
	case <-r.Context().Done():
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("<!doctype html><meta charset=\"utf-8\"><title>cumin setup</title><p>cumin got the answer of GitHub. Go back to the terminal. You can close this tab.</p>\n"))
}
