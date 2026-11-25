// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// errorCodeSpec is the enum of the Error schema's code property.
type errorCodeSpec struct {
	Enum []apierror.Code `json:"enum"`
}

// errorPropsSpec is the Error schema's properties.
type errorPropsSpec struct {
	Code errorCodeSpec `json:"code"`
}

// errorSchemaSpec is the Error schema.
type errorSchemaSpec struct {
	Properties errorPropsSpec `json:"properties"`
}

// errorSchemasSpec is the components' schemas, of which only Error is read.
type errorSchemasSpec struct {
	Error errorSchemaSpec `json:"Error"`
}

// errorComponentsSpec is openapi.json's components section.
type errorComponentsSpec struct {
	Schemas errorSchemasSpec `json:"schemas"`
}

// errorSpec is the part of openapi.json the error-code contract reads.
type errorSpec struct {
	Components errorComponentsSpec `json:"components"`
}

// The UI translates an error by its code, so openapi.json lists exactly the codes the
// speaker writes, in the order apierror.Codes keeps them.
func TestOpenAPIListsEveryErrorCode(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var spec errorSpec

	err = json.Unmarshal(raw, &spec)
	if err != nil {
		t.Fatal(err)
	}

	if got := spec.Components.Schemas.Error.Properties.Code.Enum; !slices.Equal(got, apierror.Codes()) {
		t.Errorf("openapi.json Error.code enum %v, apierror.Codes %v", got, apierror.Codes())
	}
}
