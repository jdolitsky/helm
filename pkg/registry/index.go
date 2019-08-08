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

package registry // import "helm.sh/helm/pkg/registry"

import (
	//"crypto/sha1"
	"crypto/sha256"
	//"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	//"fmt"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"io/ioutil"
	"os"
	"path/filepath"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	OCIIndexFilename = "index.json"
)

type (
	// OCIIndexOptions is used to construct a new OCIIndex
	OCIIndexOptions struct {
		RootDir      string
		LoadIfExists bool
	}

	// OCIIndex is a wrapper on the OCI index
	OCIIndex struct {
		*ocispec.Index
		RootDir string `json:"-"`
	}

	// OCIManifest is a wrapper on the OCI manifest
	OCIManifest struct {
		ocispec.Descriptor
	}
)

func NewOCIIndex(options *OCIIndexOptions) (*OCIIndex, error) {
	index := OCIIndex{
		Index:   &ocispec.Index{
			Versioned: specs.Versioned{
				SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
			},
		},
		RootDir: options.RootDir,
	}
	if options.LoadIfExists && index.RootDir != "" {
		indexPath := index.GetPath()
		if _, err := os.Stat(index.GetPath()); err == nil {
			indexRaw, err := ioutil.ReadFile(indexPath)
			if err != nil {
				return nil, err
			}
			err = json.Unmarshal(indexRaw, &index)
			if err != nil {
				return nil, err
			}
		}
	}
	return &index, nil
}

func (index *OCIIndex) GetPath() string {
	return filepath.Join(index.RootDir, OCIIndexFilename)
}

func (index *OCIIndex) AddManifest(config ocispec.Descriptor, layers []ocispec.Descriptor, ref string) ([]byte, string, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2, // historical value. does not pertain to OCI or docker version
		},
		Config: config,
		Layers: layers,
	}

	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}

	manifestDescriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestRaw),
		Size:      int64(len(manifestRaw)),
		Annotations: map[string]string{
			ocispec.AnnotationRefName: ref,
		},
	}

	index.Manifests = append(index.Manifests, manifestDescriptor)
	return manifestRaw, manifestDescriptor.Digest.Hex(), nil
}

func (index *OCIIndex) StoreBlob(blob []byte) (string, error) {
	if index.RootDir == "" {
		return "", errors.New("could not store content due to missing index root dir")
	}
	digest := index.getBlobDigest(blob)
	path, err := index.getBlobPath(digest)
	if err != nil {
		return "", err
	}
	err = os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return "", err
	}
	err = ioutil.WriteFile(path, blob, 0755)
	return digest, err
}

func (index *OCIIndex) FetchBlob(digest string) ([]byte, error) {
	path, err := index.getBlobPath(digest)
	if err != nil {
		return nil, err
	}
	blob, err := ioutil.ReadFile(path)
	return blob, err
}

func (index *OCIIndex) DeleteBlob(digest string) ([]byte, error) {
	path, err := index.getBlobPath(digest)
	if err != nil {
		return nil, err
	}
	blob, err := ioutil.ReadFile(path)
	return blob, err
}

func (index *OCIIndex) GetManifestByRef(ref string) (ocispec.Manifest, bool) {
	var manifest OCIManifest
	var exists bool
	for _, m := range index.Manifests {
		if r, ok := m.Annotations[ocispec.AnnotationRefName]; ok {
			if r == ref {
				manifest = OCIManifest{m}
				exists = true
			}
		}
	}
	r, _ := index.FetchBlob(manifest.Descriptor.Digest.Hex())

	var m ocispec.Manifest
	_ = json.Unmarshal(r, &m)
	return m, exists
}

func (index *OCIIndex) DeleteManifestByRef(ref string) (*OCIManifest, bool) {
	var newManifests []ocispec.Descriptor
	var manifest *OCIManifest
	var deleted bool
	for _, m := range index.Manifests {
		if r, ok := manifest.Annotations[ocispec.AnnotationRefName]; ok {
			if r == ref {
				manifest = &OCIManifest{m}
				deleted = true
			} else {
				newManifests = append(newManifests, m)
			}
		}
	}
	index.Manifests = newManifests
	return manifest, deleted
}

func (index *OCIIndex) Save() error {
	if index.RootDir == "" {
		return errors.New("could not save due to missing index root dir")
	}

	// Create "oci-layout" file if it doesn't already exist
	err := index.ensureOCILayoutFile()
	if err != nil {
		return err
	}

	// Write to index.json
	indexPath := filepath.Join(index.RootDir, OCIIndexFilename)
	indexRaw, err := json.Marshal(index)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(indexPath, indexRaw, 0755)
	return err
}

func (index *OCIIndex) ensureOCILayoutFile() error {
	layoutPath := filepath.Join(index.RootDir, ocispec.ImageLayoutFile)
	if _, err := os.Stat(layoutPath); os.IsNotExist(err) {
		layout := &ocispec.ImageLayout{
			Version: ocispec.ImageLayoutVersion,
		}
		layoutRaw, err := json.Marshal(&layout)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(layoutPath, layoutRaw, 0755)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (index *OCIIndex) getBlobDigest(blob []byte) string {
	hasher := sha256.New()
	hasher.Write(blob)
	digest := hex.EncodeToString(hasher.Sum(nil)[:])
	return digest
}

func (index *OCIIndex) getBlobPath(digest string) (string, error) {
	if index.RootDir == "" {
		return "", errors.New("could not determine blob path due to missing index root dir")
	}
	path := filepath.Join(index.RootDir, "blobs", "sha256", digest)
	return path, nil
}
