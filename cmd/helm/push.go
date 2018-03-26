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

package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/repo"
)

const pushDesc = `
Push a package to a remote repository.
`

type pushCmd struct {
	packagePath string
	repoName    string
	namespace   string

	out  io.Writer
	home helmpath.Home
}

func newPushCmd(out io.Writer) *cobra.Command {
	push := &pushCmd{
		out:  out,
		home: settings.Home,
	}

	cmd := &cobra.Command{
		Use:   "push [flags] [PACKAGE_PATH] [REPO_NAME] [NAMESPACE] [...]",
		Short: "push a package to a remote repository",
		Long:  pushDesc,
		RunE: func(cmd *cobra.Command, args []string) error {
			numArgs := len(args)
			requiredArgs := []string{"path to chart archive", "name of the chart repository"}
			if err := checkArgsLength(numArgs, requiredArgs...); err != nil {
				if err := checkArgsLength(numArgs-1, requiredArgs...); err != nil {
					return err
				}
				push.namespace = args[2]
			}

			push.packagePath = args[0]
			push.repoName = args[1]

			err := push.run()
			return err
		},
	}

	return cmd
}

func (p *pushCmd) run() error {
	packageAbsPath, err := filepath.Abs(p.packagePath)
	if err != nil {
		return err
	}

	repoFile := p.home.RepositoryFile()
	r, err := repo.LoadRepositoriesFile(repoFile)
	if err != nil {
		return err
	}

	repoName := p.repoName
	repository, exists := r.Get(repoName)
	if !exists {
		return fmt.Errorf("no repo named %q found", repoName)
	}

	return repository.Push(packageAbsPath, p.namespace)
}
