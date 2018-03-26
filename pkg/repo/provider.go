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

package repo // import "k8s.io/helm/pkg/repo"

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/helm/pkg/repo/config"
	"k8s.io/helm/pkg/repo/providers/chartmuseum"
)

type (
	// Provider supplies additional repo functionality:
	// - push/upload of charts via PushChart method (helm push)
	Provider interface {
		Init(*config.Entry) error
		Push(chartAbsPath string, namespace string) error
	}
)

var (
	providerImplMap = map[string]Provider{
		"chartmuseum": Provider(new(chartmuseum.ChartMuseum)),
	}
)

// GetProvider returns appropriate provider based on repo entry config
func (cfg *Entry) GetProvider() (Provider, error) {
	var provider Provider
	var err error
	var exists bool

	provider, exists = providerImplMap[strings.ToLower(cfg.Provider)]

	if exists {
		err = provider.Init(&config.Entry{Name: cfg.Name, Cache: cfg.Cache, URL: cfg.URL, Username: cfg.Username, Password: cfg.Password, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, CAFile: cfg.CAFile, Provider: cfg.Provider})
	} else if cfg.Provider == "" {
		err = errors.New("this method requires a repo provider, re-add repo with --provider flag")
	} else {
		err = errors.New(fmt.Sprintf("this method not supported by repo provider \"%s\"", cfg.Provider))
	}

	return provider, err
}
