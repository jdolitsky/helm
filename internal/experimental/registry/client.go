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
	"io"
	"path/filepath"

	auth "github.com/deislabs/oras/pkg/auth/docker"
	"github.com/gosuri/uitable"

	"helm.sh/helm/pkg/chart"
	"helm.sh/helm/pkg/helmpath"
)

const (
	CredentialsFileBasename = "config.json"

	ociManifestSchemaVersion = 2
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
	return nil
}

// PullChart downloads a chart from a registry
func (c *Client) PullChart(ref *Reference) error {
	return nil
}

// SaveChart stores a copy of chart in local cache
func (c *Client) SaveChart(ch *chart.Chart, ref *Reference) error {
	content, _, err := c.cache.StoreChartAtRef(ch, ref.FullName())
	fmt.Fprintf(c.out, "ref:     %s\n", ref.FullName())
	fmt.Fprintf(c.out, "digest:  %s\n", content.Digest.Hex())
	fmt.Fprintf(c.out, "size:    %s\n", byteCountBinary(content.Size))
	fmt.Fprintf(c.out, "name:    %s\n", ch.Metadata.Name)
	fmt.Fprintf(c.out, "version: %s\n", ch.Metadata.Version)
	return err
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
	_, err := c.cache.RemoveChartByRef(ref.FullName())
	return err
}

// PrintChartTable prints a list of locally stored charts
func (c *Client) PrintChartTable() error {
	table := uitable.New()
	table.MaxColWidth = 60
	table.AddRow("REF", "NAME", "VERSION", "DIGEST", "SIZE", "CREATED")
	fmt.Fprintln(c.out, table.String())
	return nil
}
