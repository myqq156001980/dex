package server

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"text/template"
)

const (
	tmplApproval = "approval.html"
	tmplLogin    = "login.html"
	tmplPassword = "password.html"
	tmplOOB      = "oob.html"
)

const coreOSLogoURL = "https://coreos.com/assets/images/brand/coreos-wordmark-135x40px.png"

var requiredTmpls = []string{
	tmplApproval,
	tmplLogin,
	tmplPassword,
	tmplOOB,
}

// TemplateConfig describes.
type TemplateConfig struct {
	// TODO(ericchiang): Asking for a directory with a set of templates doesn't indicate
	// what the templates should look like and doesn't allow consumers of this package to
	// provide their own templates in memory. In the future clean this up.

	// Directory of the templates. If empty, these will be loaded from memory.
	Dir string `yaml:"dir"`

	// Defaults to the CoreOS logo and "dex".
	LogoURL string `yaml:"logoURL"`
	Issuer  string `yaml:"issuerName"`
}

type globalData struct {
	LogoURL string
	Issuer  string
}

func loadTemplates(config TemplateConfig) (*templates, error) {
	var tmpls *template.Template
	if config.Dir != "" {
		files, err := ioutil.ReadDir(config.Dir)
		if err != nil {
			return nil, fmt.Errorf("read dir: %v", err)
		}
		filenames := []string{}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			filenames = append(filenames, filepath.Join(config.Dir, file.Name()))
		}
		if len(filenames) == 0 {
			return nil, fmt.Errorf("no files in template dir %s", config.Dir)
		}
		if tmpls, err = template.ParseFiles(filenames...); err != nil {
			return nil, fmt.Errorf("parse files: %v", err)
		}
	} else {
		// Load templates from memory. This code is largely copied from the standard library's
		// ParseFiles source code.
		// See: https://goo.gl/6Wm4mN
		for name, data := range defaultTemplates {
			var t *template.Template
			if tmpls == nil {
				tmpls = template.New(name)
			}
			if name == tmpls.Name() {
				t = tmpls
			} else {
				t = tmpls.New(name)
			}
			if _, err := t.Parse(data); err != nil {
				return nil, fmt.Errorf("parsing %s: %v", name, err)
			}
		}
	}

	missingTmpls := []string{}
	for _, tmplName := range requiredTmpls {
		if tmpls.Lookup(tmplName) == nil {
			missingTmpls = append(missingTmpls, tmplName)
		}
	}
	if len(missingTmpls) > 0 {
		return nil, fmt.Errorf("missing template(s): %s", missingTmpls)
	}

	if config.LogoURL == "" {
		config.LogoURL = coreOSLogoURL
	}
	if config.Issuer == "" {
		config.Issuer = "dex"
	}

	return &templates{
		globalData:   config,
		loginTmpl:    tmpls.Lookup(tmplLogin),
		approvalTmpl: tmpls.Lookup(tmplApproval),
		passwordTmpl: tmpls.Lookup(tmplPassword),
		oobTmpl:      tmpls.Lookup(tmplOOB),
	}, nil
}

var scopeDescriptions = map[string]string{
	"offline_access": "Have offline access",
	"profile":        "View basic profile information",
	"email":          "View your email",
}

type templates struct {
	globalData   TemplateConfig
	loginTmpl    *template.Template
	approvalTmpl *template.Template
	passwordTmpl *template.Template
	oobTmpl      *template.Template
}

type connectorInfo struct {
	ID   string
	Name string
	URL  string
}

type byName []connectorInfo

func (n byName) Len() int           { return len(n) }
func (n byName) Less(i, j int) bool { return n[i].Name < n[j].Name }
func (n byName) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }

func (t *templates) login(w http.ResponseWriter, connectors []connectorInfo, authReqID string) {
	sort.Sort(byName(connectors))

	data := struct {
		TemplateConfig
		Connectors []connectorInfo
		AuthReqID  string
	}{t.globalData, connectors, authReqID}
	renderTemplate(w, t.loginTmpl, data)
}

func (t *templates) password(w http.ResponseWriter, authReqID, callback, lastUsername string, lastWasInvalid bool) {
	data := struct {
		TemplateConfig
		AuthReqID string
		PostURL   string
		Username  string
		Invalid   bool
	}{t.globalData, authReqID, callback, lastUsername, lastWasInvalid}
	renderTemplate(w, t.passwordTmpl, data)
}

func (t *templates) approval(w http.ResponseWriter, authReqID, username, clientName string, scopes []string) {
	accesses := []string{}
	for _, scope := range scopes {
		access, ok := scopeDescriptions[scope]
		if ok {
			accesses = append(accesses, access)
		}
	}
	sort.Strings(accesses)
	data := struct {
		TemplateConfig
		User      string
		Client    string
		AuthReqID string
		Scopes    []string
	}{t.globalData, username, clientName, authReqID, accesses}
	renderTemplate(w, t.approvalTmpl, data)
}

func (t *templates) oob(w http.ResponseWriter, code string) {
	data := struct {
		TemplateConfig
		Code string
	}{t.globalData, code}
	renderTemplate(w, t.oobTmpl, data)
}

// small io.Writer utilitiy to determine if executing the template wrote to the underlying response writer.
type writeRecorder struct {
	wrote bool
	w     io.Writer
}

func (w *writeRecorder) Write(p []byte) (n int, err error) {
	w.wrote = true
	return w.w.Write(p)
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, data interface{}) {
	wr := &writeRecorder{w: w}
	if err := tmpl.Execute(wr, data); err != nil {
		log.Printf("Error rendering template %s: %s", tmpl.Name(), err)

		if !wr.wrote {
			// TODO(ericchiang): replace with better internal server error.
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
	return
}
