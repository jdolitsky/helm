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

package registry // import "helm.sh/helm/internal/experimental/registry"

import (
	orascontent "github.com/deislabs/oras/pkg/content"
)

type (
	// Cache handles local/in-memory storage of Helm charts
	Cache struct {
		ociStore    *orascontent.OCIStore
		memoryStore *orascontent.Memorystore
		rootDir     string
	}
)

func NewCache(rootDir string) (*Cache, error) {
	ociStore, err := orascontent.NewOCIStore(rootDir)
	if err != nil {
		return nil, err
	}
	cache := Cache{
		ociStore:    ociStore,
		memoryStore: orascontent.NewMemoryStore(),
		rootDir:     rootDir,
	}
	return &cache, nil
}
