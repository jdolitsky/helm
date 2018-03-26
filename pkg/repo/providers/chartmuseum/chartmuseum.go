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
	"k8s.io/helm/pkg/repo/config"
)

type (
	// ChartMuseum is a repo provider for the ChartMuseum web server.
	ChartMuseum struct {
		Config *config.Entry
	}

	errorResponse struct {
		Error string `json:"error"`
	}
)

// Init configures a ChartMuseum instance from repo config.
func (cm *ChartMuseum) Init(config *config.Entry) error {
	cm.Config = config
	return nil
}
