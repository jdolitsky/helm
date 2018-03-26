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

package chartmuseum // import "k8s.io/helm/pkg/repo/provider/chartmuseum"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"k8s.io/helm/pkg/chartutil"
	"k8s.io/helm/pkg/version"
)

// Push uploads a chart package to a ChartMuseum web server.
func (cm *ChartMuseum) Push(packageAbsPath string, namespace string) error {
	chart, err := chartutil.LoadFile(packageAbsPath)
	if err != nil {
		return err
	}

	meta := chart.GetMetadata()
	msg := fmt.Sprintf("Pushing chart %s version %s to repo %s", meta.Name, meta.Version, cm.Config.Name)
	if namespace != "" {
		msg += fmt.Sprintf("[%s]", namespace)
	}
	fmt.Println(msg + "...")

	return uploadPackage(packageAbsPath, cm.Config.URL, namespace, cm.Config.Username, cm.Config.Password)
}

func uploadPackage(packageAbsPath string, endpoint string, namespace string, username string, password string) error {
	client := &http.Client{}

	u, err := url.Parse(endpoint)
	u.Path = path.Join(u.Path, "api", namespace, "charts")
	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return err
	}

	err = setUploadPackageRequestBody(req, packageAbsPath)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Helm/"+strings.TrimPrefix(version.GetVersion(), "v"))

	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 201 {
		b, err := ioutil.ReadAll(resp.Body)
		defer resp.Body.Close()
		if err != nil {
			return err
		}
		var er errorResponse
		err = json.Unmarshal(b, &er)
		if err != nil || er.Error == "" {
			return errors.New(fmt.Sprintf("%d: could not properly parse response JSON: %s",
				resp.StatusCode, string(b)))
		}
		return errors.New(fmt.Sprintf("%d: %s", resp.StatusCode, er.Error))
	}

	fmt.Println("Done.")
	return nil
}

func setUploadPackageRequestBody(req *http.Request, packageAbsPath string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	defer w.Close()
	fw, err := w.CreateFormFile("chart", packageAbsPath)
	if err != nil {
		return err
	}
	w.FormDataContentType()
	fd, err := os.Open(packageAbsPath)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = io.Copy(fw, fd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Body = ioutil.NopCloser(&body)
	return nil
}
