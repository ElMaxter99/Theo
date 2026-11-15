package capture

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

// Detection is a proposal for the "run" section of .theo.json, with why.
// It is only a proposal: a person accepts it, and Theo checks it by starting
// the application before it is saved.
type Detection struct {
	Run        *repoconfig.RunSpec `json:"run,omitempty"`
	Confidence string              `json:"confidence"` // high, medium, low or none
	Evidence   []string            `json:"evidence,omitempty"`
	Notes      []string            `json:"notes,omitempty"`
}

var composeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// Detect looks at a project folder and proposes how to start it, with fixed
// rules (no AI): Docker Compose, then a package.json script, then a Makefile.
// A "run" section the repository already declares is returned as it is.
func Detect(root string) (Detection, error) {
	if l, err := repoconfig.Load(root); err != nil {
		return Detection{}, err
	} else if l.Run != nil {
		return Detection{Run: l.Run, Confidence: "high", Evidence: []string{repoconfig.FileName + " already has a run section"}}, nil
	}

	var d Detection
	for _, f := range composeFiles {
		if exists(filepath.Join(root, f)) {
			d.Run = &repoconfig.RunSpec{Kind: repoconfig.RunCompose}
			d.Confidence = "high"
			d.Evidence = append(d.Evidence, f)
			d.Notes = append(d.Notes, "set run.service if the web is not the first service")
			break
		}
	}

	node, nerr := detectNode(root)
	if nerr != nil {
		return d, nerr
	}
	switch {
	case node != nil && d.Run == nil:
		return *node, nil
	case node != nil:
		d.Notes = append(d.Notes, "package.json also has a start script ("+node.Run.Start+"); Compose was preferred")
		return d, nil
	case d.Run != nil:
		return d, nil
	}

	if mk := detectMake(root); mk != nil {
		return *mk, nil
	}
	return Detection{Confidence: "none", Notes: []string{"No docker-compose file, package.json start script or Makefile target found; write the run section by hand (see docs/cli/tools.md)"}}, nil
}

type packageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func detectNode(root string) (*Detection, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return &Detection{Confidence: "none", Notes: []string{"package.json is not valid JSON: " + err.Error()}}, nil
	}
	if d := detectAngularJS(root, pkg); d != nil {
		return d, nil
	}
	script := ""
	for _, s := range []string{"dev", "start", "serve"} {
		if _, ok := pkg.Scripts[s]; ok {
			script = s
			break
		}
	}
	if script == "" {
		return nil, nil
	}
	has := func(name string) bool {
		_, a := pkg.Dependencies[name]
		_, b := pkg.DevDependencies[name]
		return a || b
	}

	pm, install := "npm", "npm install"
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		pm, install = "pnpm", "pnpm install --frozen-lockfile"
	case exists(filepath.Join(root, "yarn.lock")):
		pm, install = "yarn", "yarn install --frozen-lockfile"
	case exists(filepath.Join(root, "package-lock.json")):
		install = "npm ci"
	}
	run := pm + " run " + script
	if pm == "yarn" {
		run = "yarn " + script
	}

	d := &Detection{Confidence: "medium", Evidence: []string{"package.json: scripts." + script}}
	spec := &repoconfig.RunSpec{Kind: repoconfig.RunScript, Install: []string{install}}
	// Tools that read the port from a flag get it there; the rest from PORT.
	switch {
	case has("vite") || has("@sveltejs/kit"):
		spec.Start = run + " -- --port ${PORT} --strictPort"
		d.Evidence = append(d.Evidence, "vite dependency: port by --port")
	case has("next"):
		spec.Start = run + " -- -p ${PORT}"
		d.Evidence = append(d.Evidence, "next dependency: port by -p")
	case has("@angular/cli"):
		spec.Start = run + " -- --port ${PORT}"
		d.Evidence = append(d.Evidence, "angular dependency: port by --port")
	default:
		spec.Start = run
		d.Notes = append(d.Notes, "The framework was not recognised; Theo gives the port in PORT. If the script ignores it, add the port to the start command")
		d.Confidence = "low"
	}
	d.Run = spec
	return d, nil
}

func detectMake(root string) *Detection {
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return nil
	}
	for _, target := range []string{"dev", "run", "serve", "start"} {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, target+":") {
				return &Detection{
					Run:        &repoconfig.RunSpec{Kind: repoconfig.RunScript, Start: "make " + target},
					Confidence: "low",
					Evidence:   []string{"Makefile target " + target},
					Notes:      []string{"Theo gives the port in PORT; check that the target uses it"},
				}
			}
		}
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var baseHref = regexp.MustCompile(`(?i)<base\s[^>]*href\s*=\s*["']([^"']+)["']`)

// detectAngularJS recognises an AngularJS (1.x) application whose build only
// leaves files in a folder, and proposes serving that folder under the base
// path its index.html declares.
func detectAngularJS(root string, pkg packageJSON) *Detection {
	ver, ok := pkg.Dependencies["angular"]
	if !ok {
		ver, ok = pkg.DevDependencies["angular"]
	}
	if !ok {
		return nil
	}
	if _, ok := pkg.Scripts["build"]; !ok {
		return nil
	}
	install, build := "npm install", "npm run build"
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		install, build = "pnpm install --frozen-lockfile", "pnpm run build"
	case exists(filepath.Join(root, "yarn.lock")):
		install, build = "yarn install --frozen-lockfile", "yarn build"
	case exists(filepath.Join(root, "package-lock.json")):
		install = "npm ci"
	}
	dir := "dist"
	for _, d := range []string{"dist", "build", "www"} {
		if exists(filepath.Join(root, d)) {
			dir = d
			break
		}
	}
	d := &Detection{Confidence: "medium"}
	d.Evidence = append(d.Evidence, "package.json: angular "+strings.TrimLeft(ver, "^~=v ")+" (AngularJS), scripts.build")
	base := "/"
	for _, f := range []string{"src/index.html", "app/index.html", "index.html", "src/app/index.html"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			continue
		}
		if m := baseHref.FindSubmatch(data); m != nil {
			if h := string(m[1]); strings.HasPrefix(h, "/") && len(h) > 1 {
				base = strings.TrimRight(h, "/")
				d.Evidence = append(d.Evidence, f+": <base href=\""+string(m[1])+"\">")
				break
			}
		}
	}
	spec := &repoconfig.RunSpec{Kind: repoconfig.RunStatic, Install: []string{install}, Build: []string{build}, Dir: dir, Base: base}
	if base != "/" {
		spec.Ready = &repoconfig.RunReady{Path: base + "/"}
	}
	d.Run = spec
	d.Notes = append(d.Notes, "Theo builds the app and serves "+dir+"/ itself under "+base+": the project\nneeds no server. Add \"spa\": true if the app uses HTML5 routes (no #)")
	return d
}
