/*
Copyright 2016 The Fission Authors.

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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	uuid "github.com/satori/go.uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fission/fission"
	"github.com/fission/fission/controller/client"
	storageSvcClient "github.com/fission/fission/storagesvc/client"
	"github.com/fission/fission/tpr"
)

func fatal(msg string) {
	os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}

func getClient(serverUrl string) *client.Client {

	if len(serverUrl) == 0 {
		fatal("Need --server or FISSION_URL set to your fission server.")
	}

	isHTTPS := strings.Index(serverUrl, "https://") == 0
	isHTTP := strings.Index(serverUrl, "http://") == 0

	if !(isHTTP || isHTTPS) {
		serverUrl = "http://" + serverUrl
	}

	return client.MakeClient(serverUrl)
}

func checkErr(err error, msg string) {
	if err != nil {
		fatal(fmt.Sprintf("Failed to %v: %v", msg, err))
	}
}

func fileSize(filePath string) int64 {
	info, err := os.Stat(filePath)
	checkErr(err, fmt.Sprintf("stat %v", filePath))
	return info.Size()
}

// upload a file and return a fission.Archive
func createArchive(client *client.Client, fileName string) *fission.Archive {
	var archive fission.Archive
	if fileSize(fileName) < fission.ArchiveLiteralSizeLimit {
		contents := getContents(fileName)
		archive.Type = fission.ArchiveTypeLiteral
		archive.Literal = contents
	} else {
		u := strings.TrimSuffix(client.Url, "/") + "/proxy/storage"
		ssClient := storageSvcClient.MakeClient(u)

		// TODO add a progress bar
		id, err := ssClient.Upload(fileName, nil)
		checkErr(err, fmt.Sprintf("upload file %v", fileName))

		archiveUrl := ssClient.GetUrl(id)

		archive.Type = fission.ArchiveTypeUrl
		archive.URL = archiveUrl

		f, err := os.Open(fileName)
		if err != nil {
			checkErr(err, fmt.Sprintf("find file %v", fileName))
		}
		defer f.Close()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			checkErr(err, fmt.Sprintf("calculate checksum for file %v", fileName))
		}

		archive.Checksum = fission.Checksum{
			Type: fission.ChecksumTypeSHA256,
			Sum:  hex.EncodeToString(h.Sum(nil)),
		}
	}
	return &archive
}

func createPackage(client *client.Client, envName, srcArchiveName, deployArchiveName, buildcmd, description string) *metav1.ObjectMeta {
	pkgSpec := fission.PackageSpec{
		Environment: fission.EnvironmentReference{
			Namespace: metav1.NamespaceDefault,
			Name:      envName,
		},
		Description: description,
	}
	var pkgStatus fission.BuildStatus = fission.BuildStatusSucceeded

	if len(deployArchiveName) > 0 {
		pkgSpec.Deployment = *createArchive(client, deployArchiveName)
		if len(srcArchiveName) > 0 {
			fmt.Println("Deployment may be overwritten by builder manager after source package compilation")
		}
	}
	if len(srcArchiveName) > 0 {
		pkgSpec.Source = *createArchive(client, srcArchiveName)
		// set pending status to package
		pkgStatus = fission.BuildStatusPending
	}

	if len(buildcmd) > 0 {
		pkgSpec.BuildCommand = buildcmd
	}

	pkgName := strings.ToLower(uuid.NewV4().String())
	pkg := &tpr.Package{
		Metadata: metav1.ObjectMeta{
			Name:      pkgName,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: pkgSpec,
		Status: fission.PackageStatus{
			BuildStatus: pkgStatus,
		},
	}
	pkgMetadata, err := client.PackageCreate(pkg)
	checkErr(err, "create package")
	return pkgMetadata
}

func getContents(filePath string) []byte {
	var code []byte
	var err error

	code, err = ioutil.ReadFile(filePath)
	checkErr(err, fmt.Sprintf("read %v", filePath))
	return code
}
