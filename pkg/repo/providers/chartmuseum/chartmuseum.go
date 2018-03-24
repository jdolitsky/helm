/*
Copyright 2018 The Kubernetes Authors All rights reserved.

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

package chartmuseum // import "k8s.io/helm/pkg/repo/providers/chartmuseum"

import (
	"fmt"
	"path/filepath"

	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/repo/config"
)

type (
	Provider struct {
		Config *config.Entry
	}
)

func (p *Provider) PushChart(absPath string, repoDestPath string) error {
	chart, err := chartutil.LoadFile(absPath)
	if err != nil {
		return err
	}

	var extraMsg string
	if repoDestPath != "" {
		extraMsg = fmt.Sprintf(" at path %s", repoDestPath)
	}
	meta := chart.GetMetadata()
	fmt.Println(fmt.Sprintf("Pushing %s version %s to %s%s... ",
		meta.Name, meta.Version, p.Config.Name, extraMsg))

	endpoint := filepath.Join(p.Config.URL, "api", repoDestPath, "charts")
	fmt.Println(endpoint)

	fmt.Println("Done.")
	return nil
}
