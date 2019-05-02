/*
 * Copyright 2018 Intel Corporation, Inc
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package connection

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
)

// ConnectionHandler is used to store backend implementations objects
// Also simplifies mocking for unit testing purposes
type ConnectionHandler struct {
	// Interface that implements Connectivity operations
	// We will set this variable with a mock interface for testing
	Client ConnectionManager
}

// CreateHandler handles creation of the connectivity entry in the database
// This is a multipart handler. See following example curl request
// curl -i -F "metadata={\"cloud-region\":\"kud\",\"cloud-owner\":\"me\"};type=application/json" \
//         -F file=@/home/user/.kube/config \
//         -X POST http://localhost:8081/v1/connectivity-info
func (h ConnectionHandler) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var v Connection

	// Implemenation using multipart form
	// Review and enable/remove at a later date
	// Set Max size to 16mb here
	err := r.ParseMultipartForm(16777216)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	jsn := bytes.NewBuffer([]byte(r.FormValue("metadata")))
	err = json.NewDecoder(jsn).Decode(&v)
	switch {
	case err == io.EOF:
		http.Error(w, "Empty body", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Name is required.
	if v.CloudRegion == "" {
		http.Error(w, "Missing name in POST request", http.StatusBadRequest)
		return
	}

	// Cloudowner is required.
	if v.CloudOwner == "" {
		http.Error(w, "Missing cloudowner in POST request", http.StatusBadRequest)
		return
	}

	//Read the file section and ignore the header
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Unable to process file", http.StatusUnprocessableEntity)
		return
	}

	defer file.Close()

	//Convert the file content to base64 for storage
	content, err := ioutil.ReadAll(file)
	if err != nil {
		http.Error(w, "Unable to read file", http.StatusUnprocessableEntity)
		return
	}

	v.Kubeconfig = base64.StdEncoding.EncodeToString(content)

	ret, err := h.Client.Create(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(ret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// getHandler handles GET operations on a particular name
// Returns a  Connectivity instance
func (h ConnectionHandler) GetHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["connname"]

	ret, err := h.Client.Get(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(ret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// deleteHandler handles DELETE operations on a particular record
func (h ConnectionHandler) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["connname"]

	err := h.Client.Delete(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
