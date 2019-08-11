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
	"context"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"path/filepath"
	"sort"
	"time"

	auth "github.com/deislabs/oras/pkg/auth/docker"
	"github.com/deislabs/oras/pkg/oras"
	"github.com/docker/go-units"
	"github.com/gosuri/uitable"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/helmpath"
)

const (
	CredentialsFileBasename = "config.json"
)

type (
	// ClientOptions is used to construct a new client
	ClientOptions struct {
		Debug      bool
		Out        io.Writer
		Authorizer Authorizer
		Resolver   Resolver
		Cache      Cache
	}

	// Client works with OCI-compliant registries and local Helm chart cache
	Client struct {
		debug      bool
		out        io.Writer
		authorizer Authorizer
		resolver   Resolver
		cache      Cache
	}
)

// NewClient returns a new registry client with config
func NewClient(options *ClientOptions) (*Client, error) {
	client := &Client{
		debug:      options.Debug,
		out:        options.Out,
		resolver:   options.Resolver,
		authorizer: options.Authorizer,
		cache:      options.Cache,
	}
	return client, nil
}

// NewClient returns a new registry client with config
func NewClientWithDefaults() (*Client, error) {
	credentialsFile := filepath.Join(helmpath.Registry(), CredentialsFileBasename)
	authClient, err := auth.NewClient(credentialsFile)
	if err != nil {
		return nil, err
	}
	resolver, err := authClient.Resolver(context.Background())
	if err != nil {
		return nil, err
	}
	cache, err := NewCache(&CacheOptions{
		RootDir: filepath.Join(helmpath.Registry(), CacheRootDir),
	})
	if err != nil {
		return nil, err
	}
	return NewClient(&ClientOptions{
		Authorizer: Authorizer{
			Client: authClient,
		},
		Resolver: Resolver{
			Resolver: resolver,
		},
		Cache: *cache,
	})
}

// SetDebug sets the client debug setting
func (c *Client) SetDebug(debug bool) {
	c.debug = debug
	c.cache.debug = debug
}

// SetWriter sets the client writer (for logging etc)
func (c *Client) SetWriter(out io.Writer) {
	c.out = out
	c.cache.out = out
}

// Login logs into a registry
func (c *Client) Login(hostname string, username string, password string) error {
	err := c.authorizer.Login(ctx(c.out, c.debug), hostname, username, password)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "Login succeeded")
	return nil
}

// Logout logs out of a registry
func (c *Client) Logout(hostname string) error {
	err := c.authorizer.Logout(ctx(c.out, c.debug), hostname)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "Logout succeeded")
	return nil
}

// PushChart uploads a chart to a registry
func (c *Client) PushChart(ref *Reference) error {
	config, layers, exists, err := c.cache.LoadChartDescriptorsByRef(ref.FullName())
	if err != nil {
		return err
	}
	if !exists {
		return errors.New(fmt.Sprintf("No such chart: %s", ref.FullName()))
	}
	_, err = oras.Push(ctx(c.out, c.debug), c.resolver, ref.FullName(), c.cache.memoryStore,
		layers, oras.WithConfig(*config), oras.WithNameValidation(nil))
	return err
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	return nil
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {
	content, _, err := c.cache.StoreChartAtRef(ch, ref.FullName())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s: saved\n", ref.Tag)
	if c.debug {
		fmt.Fprintf(c.out, "+ ref: %s\n", ref.FullName())
		fmt.Fprintf(c.out, "+ digest: %s\n", content.Digest.Hex())
		fmt.Fprintf(c.out, "+ size: %s\n", byteCountBinary(content.Size))
		fmt.Fprintf(c.out, "+ name: %s\n", ch.Metadata.Name)
		fmt.Fprintf(c.out, "+ version: %s\n", ch.Metadata.Version)
	}
	return nil
}

// LoadChart retrieves a chart object by reference
func (c *Client) LoadChart(ref *Reference) (*chart.Chart, error) {
	ch, _, err := c.cache.FetchChartByRef(ref.FullName())
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// RemoveChart deletes a locally saved chart
func (c *Client) RemoveChart(ref *Reference) error {
	exists, err := c.cache.RemoveChartByRef(ref.FullName())
	if !exists {
		return errors.New(fmt.Sprintf("No such chart: %s", ref.FullName()))
	}
	fmt.Fprintf(c.out, "%s: removed\n", ref.Tag)
	return err
}

// PrintChartTable prints a list of locally stored charts
func (c *Client) PrintChartTable() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")
	rows := c.getChartTableRows()
	for _, row := range rows {
		table.AddRow(row...)
	}
	fmt.Fprintln(c.out, table.String())
	return nil
}

// getChartTableRows returns rows in uitable-friendly format
// TODO: clean clean tidy tidy
func (c *Client) getChartTableRows() [][]interface{} {
	refsMap := map[string]map[string]string{}
	for _, manifest := range c.cache.ociStore.ListReferences() {
		ref := manifest.Annotations[ocispec.AnnotationRefName]
		if ref == "" {
			if c.debug {
				fmt.Fprintf(c.out, fmt.Sprintf("warning: found manifest with no name: %s\n", manifest.Digest))
			}
			continue
		}
		ch, err := c.cache.manifestDescriptorToChart(&manifest)
		if err != nil {
			if c.debug {
				fmt.Fprintf(c.out, fmt.Sprintf("warning: could not parse chart for %s: %s\n", ref, err.Error()))
			}
			continue
		}
		if _, ok := refsMap[ref]; !ok {
			refsMap[ref] = map[string]string{}
		}
		refsMap[ref]["name"] = ch.Metadata.Name
		refsMap[ref]["version"] = ch.Metadata.Version
		_, layers, exists, err := c.cache.LoadChartDescriptorsByRef(ref)
		if err != nil {
			if c.debug {
				fmt.Fprintf(c.out,
					fmt.Sprintf("warning: error loading chart descriptors for %s: %s\n", ref, err.Error()))
			}
			continue
		}
		if !exists {
			if c.debug {
				fmt.Fprintf(c.out,
					fmt.Sprintf("warning: chart descriptors could not be located for %s\n", ref))
			}
			continue
		}
		numLayers := len(layers)
		if numLayers < 1 {
			if c.debug {
				fmt.Fprintf(c.out,
					fmt.Sprintf("warning: chart descriptors had invalid layers (%d vs. 1) for %s: %s\n",
						numLayers, ref, err.Error()))
			}
			continue
		}
		content := layers[0]
		info, err := c.cache.ociStore.Info(ctx(c.out, c.debug), content.Digest)
		if err != nil {
			if c.debug {
				fmt.Fprintf(c.out,
					fmt.Sprintf("warning: error describing blob %s for %s: %s\n",
						content.Digest.Hex(), ref, err.Error()))
			}
			continue
		}
		refsMap[ref]["digest"] = shortDigest(info.Digest.Hex())
		refsMap[ref]["size"] = byteCountBinary(info.Size)
		refsMap[ref]["created"] = units.HumanDuration(time.Now().UTC().Sub(info.CreatedAt))
	}
	// Filter out any refs that are incomplete (do not have all required fields)
	tableHeaders := []string{"name", "version", "digest", "size", "created"}
	for k, ref := range refsMap {
		allKeysFound := true
		for _, v := range tableHeaders {
			if _, ok := ref[v]; !ok {
				allKeysFound = false
				break
			}
		}
		if !allKeysFound {
			delete(refsMap, k)
		}
	}
	// Sort and convert to format expected by uitable
	rows := make([][]interface{}, len(refsMap))
	keys := make([]string, 0, len(refsMap))
	for key := range refsMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		rows[i] = make([]interface{}, 6)
		rows[i][0] = key
		ref := refsMap[key]
		for j, k := range tableHeaders {
			rows[i][j+1] = ref[k]
		}
	}
	return rows
}
