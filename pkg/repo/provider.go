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

	"k8s.io/helm/pkg/repo/config"
	"k8s.io/helm/pkg/repo/providers/chartmuseum"
)

type (
	// Provider is a generic interface for repo providers
	Provider interface {
		PushChart(path string, repoDestPath string) error
	}
)

func (cfg *Entry) GetProvider() (Provider, error) {
	var provider Provider
	var err error

	switch cfg.Provider {
	case "chartmuseum":
		provider = &chartmuseum.Provider{
			Config: &config.Entry{
				Name:     cfg.Name,
				Cache:    cfg.Cache,
				URL:      cfg.URL,
				Username: cfg.Username,
				Password: cfg.Password,
				CertFile: cfg.CertFile,
				KeyFile:  cfg.KeyFile,
				CAFile:   cfg.CAFile,
				Provider: cfg.Provider,
			},
		}
	case "":
		err = errors.New("repo provider not set")
	default:
		err = errors.New(fmt.Sprintf("repo provider \"%s\" not  supported", cfg.Provider))
	}

	return provider, err
}
