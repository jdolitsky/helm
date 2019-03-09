/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package action

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"unicode"

	"github.com/Azure/golua/lua"
	"github.com/Azure/golua/std"
	"github.com/jdolitsky/goluamapper"
	"gopkg.in/yaml.v2"

	"k8s.io/helm/pkg/chart"
	"k8s.io/helm/pkg/chart/loader"
)

// LuaInstall performs an install of a lua-based chart.
type LuaInstall struct {
	cfg *Configuration
}

// NewLuaInstall creates a new NewLuaInstall object with the given configuration.
func NewLuaInstall(cfg *Configuration) *LuaInstall {
	return &LuaInstall{
		cfg: cfg,
	}
}

// Run executes the chart list operation
func (a *LuaInstall) Run(out io.Writer, releaseName string, chartPath string) error {
	ch, err := loadLuaChart(chartPath)
	if err != nil {
		return err
	}

	inst := NewInstall(a.cfg)
	inst.ReleaseName = releaseName
	rel, err := inst.Run(ch, map[string]interface{}{})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "NAME:   %s\n", rel.Name)
	return nil
}

func loadLuaChart(chartPath string) (*chart.Chart, error) {
	state := lua.NewState()
	defer state.Close()
	std.Open(state)

	ch, err := loader.Load(chartPath)
	if err != nil {
		return nil, err
	}

	err = state.ExecText(`
chart = {name = "{{ .Chart.Name }}"}
release = {name = "{{ .Release.Name }}"}
resources = {items = {}}
function resources.add(item)
    table.insert(resources.items, item)
end
`)

	if err != nil {
		return nil, err
	}

	os.Chdir(path.Join(chartPath, "ext"))
	files, err := ioutil.ReadDir(".")
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		filename := file.Name()
		if filepath.Ext(filename) == ".lua" {
			err = state.ExecFile(filename)
			if err != nil {
				return nil, err
			}
		}
	}

	// Extract the "resources" global var and map to resources var
	var resources struct{ Items []interface{} }
	state.GetGlobal("resources")
	mapper := goluamapper.NewMapper(goluamapper.Option{NameFunc: lowerCamelCase})
	err = mapper.Map(state.Pop(), &resources)
	if err != nil {
		return nil, err
	}

	for i, item := range resources.Items {
		y, err := yaml.Marshal(item)
		if err != nil {
			return nil, err
		}
		ch.Templates = append(ch.Templates, &chart.File{
			Name: fmt.Sprintf("templates/lua_%d.yaml", i),
			Data: y,
		})
	}

	return ch, err
}

func lowerCamelCase(s string) string {
	a := []rune(s)
	a[0] = unicode.ToLower(a[0])
	return string(a)
}
