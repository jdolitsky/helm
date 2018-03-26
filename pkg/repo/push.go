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

// Push pushes a package to a repository, if a provider is set.
func (cfg *Entry) Push(packageAbsPath string, namespace string) error {
	provider, err := cfg.GetProvider()
	if err != nil {
		return err
	}
	return provider.Push(packageAbsPath, namespace)
}
