// Copyright 2016-2022, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build (python || all) && !xplatform_acceptance

package ints

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-dap"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pygen "github.com/pulumi/pulumi/pkg/v3/codegen/python"
	"github.com/pulumi/pulumi/pkg/v3/codegen/schema"
	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	ptesting "github.com/pulumi/pulumi/sdk/v3/go/common/testing"
)

// This checks that error logs are not being emitted twice
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestMissingMainDoesNotEmitStackTrace(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "missing-main"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Stdout:        stdout,
		Stderr:        stderr,
		Quick:         true,
		ExpectFailure: true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			// ensure `  error: ` is only being shown once by the program
			assert.NotContains(t, stdout.String()+stderr.String(), "Traceback")
		},
	})
}

// TestPrintfPython tests that we capture stdout and stderr streams properly, even when the last line lacks an \n.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestPrintfPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("printf", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick:                  true,
		ExtraRuntimeValidation: printfTestValidation,
	})
}

//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestStackOutputsPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("stack_outputs", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			// Ensure the checkpoint contains a single resource, the Stack, with two outputs.
			fmt.Printf("Deployment: %v", stackInfo.Deployment)
			assert.NotNil(t, stackInfo.Deployment)
			if assert.Equal(t, 1, len(stackInfo.Deployment.Resources)) {
				stackRes := stackInfo.Deployment.Resources[0]
				assert.NotNil(t, stackRes)
				assert.Equal(t, resource.RootStackType, stackRes.URN.Type())
				assert.Equal(t, 0, len(stackRes.Inputs))
				assert.Equal(t, 2, len(stackRes.Outputs))
				assert.Equal(t, "ABC", stackRes.Outputs["xyz"])
				assert.Equal(t, float64(42), stackRes.Outputs["foo"])
			}
		},
	})
}

// TestStackOutputsProgramErrorPython tests that when a program error occurs, we update any
// updated stack outputs, but otherwise leave others untouched.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestStackOutputsProgramErrorPython(t *testing.T) {
	d := filepath.Join("stack_outputs_program_error", "python")

	validateOutputs := func(
		expected map[string]interface{},
	) func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
		return func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.Equal(t, expected, stackInfo.RootResource.Outputs)
		}
	}

	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join(d, "step1"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		ExtraRuntimeValidation: validateOutputs(map[string]interface{}{
			"xyz": "ABC",
			"foo": float64(42),
		}),
		EditDirs: []integration.EditDir{
			{
				Dir:           filepath.Join(d, "step2"),
				Additive:      true,
				ExpectFailure: true,
				ExtraRuntimeValidation: validateOutputs(map[string]interface{}{
					"xyz": "DEF",       // Expected to be updated
					"foo": float64(42), // Expected to remain the same
				}),
			},
		},
	})
}

// TestStackOutputsResourceErrorPython tests that when a resource error occurs, we update any
// updated stack outputs, but otherwise leave others untouched.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestStackOutputsResourceErrorPython(t *testing.T) {
	d := filepath.Join("stack_outputs_resource_error", "python")

	validateOutputs := func(
		expected map[string]interface{},
	) func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
		return func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.Equal(t, expected, stackInfo.RootResource.Outputs)
		}
	}

	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join(d, "step1"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: []integration.LocalDependency{
			{Package: "testprovider", Path: filepath.Join("..", "testprovider")},
		},
		Quick: true,
		ExtraRuntimeValidation: validateOutputs(map[string]interface{}{
			"xyz": "ABC",
			"foo": float64(42),
		}),
		EditDirs: []integration.EditDir{
			{
				Dir:           filepath.Join(d, "step2"),
				Additive:      true,
				ExpectFailure: true,
				// Expect the values to remain the same because the deployment ends before RegisterResourceOutputs is
				// called for the stack.
				ExtraRuntimeValidation: validateOutputs(map[string]interface{}{
					"xyz": "ABC",
					"foo": float64(42),
				}),
			},
			{
				Dir:           filepath.Join(d, "step3"),
				Additive:      true,
				ExpectFailure: true,
				// Expect the values to be updated.
				ExtraRuntimeValidation: validateOutputs(map[string]interface{}{
					"xyz": "DEF",
					"foo": float64(1),
				}),
			},
		},
	})
}

// Tests basic configuration from the perspective of a Pulumi program.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestConfigBasicPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("config_basic", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		Config: map[string]string{
			"aConfigValue": "this value is a Pythonic value",
		},
		Secrets: map[string]string{
			"bEncryptedSecret": "this super Pythonic secret is encrypted",
		},
		OrderedConfig: []integration.ConfigValue{
			{Key: "outer.inner", Value: "value", Path: true},
			{Key: "names[0]", Value: "a", Path: true},
			{Key: "names[1]", Value: "b", Path: true},
			{Key: "names[2]", Value: "c", Path: true},
			{Key: "names[3]", Value: "super secret name", Path: true, Secret: true},
			{Key: "servers[0].port", Value: "80", Path: true},
			{Key: "servers[0].host", Value: "example", Path: true},
			{Key: "a.b[0].c", Value: "true", Path: true},
			{Key: "a.b[1].c", Value: "false", Path: true},
			{Key: "tokens[0]", Value: "shh", Path: true, Secret: true},
			{Key: "foo.bar", Value: "don't tell", Path: true, Secret: true},
		},
	})
}

// Tests configuration error from the perspective of a Pulumi program.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestConfigMissingPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("config_missing", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick:         true,
		ExpectFailure: true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.NotEmpty(t, stackInfo.Events)
			text1 := "Missing required configuration variable 'config_missing_py:notFound'"
			text2 := "\tplease set a value using the command `pulumi config set --secret config_missing_py:notFound <value>`"
			var found1, found2 bool
			for _, event := range stackInfo.Events {
				if event.DiagnosticEvent != nil && strings.Contains(event.DiagnosticEvent.Message, text1) {
					found1 = true
				}
				if event.DiagnosticEvent != nil && strings.Contains(event.DiagnosticEvent.Message, text2) {
					found2 = true
				}
			}
			assert.True(t, found1, "expected error %q", text1)
			assert.True(t, found2, "expected error %q", text2)
		},
	})
}

// Tests that accessing config secrets using non-secret APIs results in warnings being logged.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestConfigSecretsWarnPython(t *testing.T) {
	// TODO[pulumi/pulumi#7127]: Re-enabled the warning.
	t.Skip("Temporarily skipping test until we've re-enabled the warning - pulumi/pulumi#7127")
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("config_secrets_warn", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		Config: map[string]string{
			"plainstr1":   "1",
			"plainstr2":   "2",
			"plainstr3":   "3",
			"plainstr4":   "4",
			"plainbool1":  "true",
			"plainbool2":  "true",
			"plainbool3":  "true",
			"plainbool4":  "true",
			"plainint1":   "1",
			"plainint2":   "2",
			"plainint3":   "3",
			"plainint4":   "4",
			"plainfloat1": "1.1",
			"plainfloat2": "2.2",
			"plainfloat3": "3.3",
			"plainfloat4": "4.4",
			"plainobj1":   "{}",
			"plainobj2":   "{}",
			"plainobj3":   "{}",
			"plainobj4":   "{}",
		},
		Secrets: map[string]string{
			"str1":   "1",
			"str2":   "2",
			"str3":   "3",
			"str4":   "4",
			"bool1":  "true",
			"bool2":  "true",
			"bool3":  "true",
			"bool4":  "true",
			"int1":   "1",
			"int2":   "2",
			"int3":   "3",
			"int4":   "4",
			"float1": "1.1",
			"float2": "2.2",
			"float3": "3.3",
			"float4": "4.4",
			"obj1":   "{}",
			"obj2":   "{}",
			"obj3":   "{}",
			"obj4":   "{}",
		},
		OrderedConfig: []integration.ConfigValue{
			{Key: "parent1.foo", Value: "plain1", Path: true},
			{Key: "parent1.bar", Value: "secret1", Path: true, Secret: true},
			{Key: "parent2.foo", Value: "plain2", Path: true},
			{Key: "parent2.bar", Value: "secret2", Path: true, Secret: true},
			{Key: "names1[0]", Value: "plain1", Path: true},
			{Key: "names1[1]", Value: "secret1", Path: true, Secret: true},
			{Key: "names2[0]", Value: "plain2", Path: true},
			{Key: "names2[1]", Value: "secret2", Path: true, Secret: true},
		},
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.NotEmpty(t, stackInfo.Events)
			//nolint:lll
			expectedWarnings := []string{
				"Configuration 'config_secrets_python:str1' value is a secret; use `get_secret` instead of `get`",
				"Configuration 'config_secrets_python:str2' value is a secret; use `require_secret` instead of `require`",
				"Configuration 'config_secrets_python:bool1' value is a secret; use `get_secret_bool` instead of `get_bool`",
				"Configuration 'config_secrets_python:bool2' value is a secret; use `require_secret_bool` instead of `require_bool`",
				"Configuration 'config_secrets_python:int1' value is a secret; use `get_secret_int` instead of `get_int`",
				"Configuration 'config_secrets_python:int2' value is a secret; use `require_secret_int` instead of `require_int`",
				"Configuration 'config_secrets_python:float1' value is a secret; use `get_secret_float` instead of `get_float`",
				"Configuration 'config_secrets_python:float2' value is a secret; use `require_secret_float` instead of `require_float`",
				"Configuration 'config_secrets_python:obj1' value is a secret; use `get_secret_object` instead of `get_object`",
				"Configuration 'config_secrets_python:obj2' value is a secret; use `require_secret_object` instead of `require_object`",
				"Configuration 'config_secrets_python:parent1' value is a secret; use `get_secret_object` instead of `get_object`",
				"Configuration 'config_secrets_python:parent2' value is a secret; use `require_secret_object` instead of `require_object`",
				"Configuration 'config_secrets_python:names1' value is a secret; use `get_secret_object` instead of `get_object`",
				"Configuration 'config_secrets_python:names2' value is a secret; use `require_secret_object` instead of `require_object`",
			}
			for _, warning := range expectedWarnings {
				var found bool
				for _, event := range stackInfo.Events {
					if event.DiagnosticEvent != nil && event.DiagnosticEvent.Severity == "warning" &&
						strings.Contains(event.DiagnosticEvent.Message, warning) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected warning %q", warning)
			}

			// These keys should not be in any warning messages.
			unexpectedWarnings := []string{
				"plainstr1",
				"plainstr2",
				"plainstr3",
				"plainstr4",
				"plainbool1",
				"plainbool2",
				"plainbool3",
				"plainbool4",
				"plainint1",
				"plainint2",
				"plainint3",
				"plainint4",
				"plainfloat1",
				"plainfloat2",
				"plainfloat3",
				"plainfloat4",
				"plainobj1",
				"plainobj2",
				"plainobj3",
				"plainobj4",
				"str3",
				"str4",
				"bool3",
				"bool4",
				"int3",
				"int4",
				"float3",
				"float4",
				"obj3",
				"obj4",
			}
			for _, warning := range unexpectedWarnings {
				for _, event := range stackInfo.Events {
					if event.DiagnosticEvent != nil {
						assert.NotContains(t, event.DiagnosticEvent.Message, warning)
					}
				}
			}
		},
	})
}

//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestResourceWithSecretSerializationPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("secret_outputs", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			// The program exports three resources:
			//   1. One named `withSecret` who's prefix property should be secret, specified via `pulumi.secret()`.
			//   2. One named `withSecretAdditional` who's prefix property should be a secret, specified via
			//      additionalSecretOutputs.
			//   3. One named `withoutSecret` which should not be a secret.
			// We serialize both of the these as plain old objects, so they appear as maps in the output.
			withSecretProps, ok := stackInfo.Outputs["withSecret"].(map[string]interface{})
			assert.Truef(t, ok, "POJO output was not serialized as a map")

			withSecretAdditionalProps, ok := stackInfo.Outputs["withSecretAdditional"].(map[string]interface{})
			assert.Truef(t, ok, "POJO output was not serialized as a map")

			withoutSecretProps, ok := stackInfo.Outputs["withoutSecret"].(map[string]interface{})
			assert.Truef(t, ok, "POJO output was not serialized as a map")

			// The secret prop should have been serialized as a secret
			secretPropValue, ok := withSecretProps["prefix"].(map[string]interface{})
			assert.Truef(t, ok, "secret output was not serialized as a secret")
			assert.Equal(t, resource.SecretSig, secretPropValue[resource.SigKey].(string))

			// The other secret prop should have been serialized as a secret
			secretAdditionalPropValue, ok := withSecretAdditionalProps["prefix"].(map[string]interface{})
			assert.Truef(t, ok, "secret output was not serialized as a secret")
			assert.Equal(t, resource.SecretSig, secretAdditionalPropValue[resource.SigKey].(string))

			// And here, the prop was not set, it should just be a string value
			_, isString := withoutSecretProps["prefix"].(string)
			assert.Truef(t, isString, "non-secret output was not a string")
		},
	})
}

// Tests that we issue an error if we fail to locate the Python command when running
// a Python example.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestPython3NotInstalled(t *testing.T) {
	// TODO[pulumi/pulumi#6304]
	t.Skip("Temporarily skipping failing test - pulumi/pulumi#6304")
	stderr := &bytes.Buffer{}
	badPython := "python3000"
	expectedError := fmt.Sprintf(
		"Failed to locate any of %q on your PATH.  Have you installed Python 3.6 or greater?",
		[]string{badPython})
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("empty", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		Env: []string{
			// Note: we use PULUMI_PYTHON_CMD to override the default behavior of searching
			// for Python 3, since anyone running tests surely already has Python 3 installed on their
			// machine. The code paths are functionally the same.
			"PULUMI_PYTHON_CMD=" + badPython,
		},
		ExpectFailure: true,
		Stderr:        stderr,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			output := stderr.String()
			assert.Contains(t, output, expectedError)
		},
	})
}

// Tests custom resource type name of dynamic provider in Python.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestCustomResourceTypeNameDynamicPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("dynamic", "python-resource-type-name"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			urnOut := stack.Outputs["urn"].(string)
			urn := resource.URN(urnOut)
			typ := urn.Type().String()
			assert.Equal(t, "pulumi-python:dynamic/custom-provider:CustomResource", typ)
		},
	})
}

// Tests dynamic provider in Python with `serialize_as_secret_always` set to `False`.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestDynamicPythonDisableSerializationAsSecret(t *testing.T) {
	dir := filepath.Join("dynamic", "python-disable-serialization-as-secret")
	var randomVal string
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: dir,
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			randomVal = stack.Outputs["random_val"].(string)
		},
		EditDirs: []integration.EditDir{{
			Dir:      filepath.Join(dir, "step1"),
			Additive: true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				assert.Equal(t, randomVal, stack.Outputs["random_val"].(string))

				// `serialize_as_secret_always` is set to `False`, so we expect `__provider` to be a plain string
				// and not a secret since it didn't capture any secrets.
				dynRes := stack.Deployment.Resources[2]
				assert.IsType(t, "", dynRes.Inputs["__provider"], "expect __provider to be a string")
				assert.IsType(t, "", dynRes.Outputs["__provider"], "expect __provider to be a string")

				// Ensure there are no diagnostic events other than debug.
				for _, event := range stack.Events {
					if event.DiagnosticEvent != nil {
						assert.Equal(t, "debug", event.DiagnosticEvent.Severity,
							"unexpected diagnostic event: %#v", event.DiagnosticEvent)
					}
				}
			},
		}},
		UseSharedVirtualEnv: boolPointer(false),
	})
}

// Tests custom resource type name of dynamic provider in Python.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestDynamicProviderSecretsPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("dynamic", "python-secrets"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Secrets: map[string]string{
			"password": "s3cret",
		},
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			// Ensure the __provider input (and corresponding output) was marked secret
			dynRes := stackInfo.Deployment.Resources[2]
			for _, providerVal := range []interface{}{dynRes.Inputs["__provider"], dynRes.Outputs["__provider"]} {
				switch v := providerVal.(type) {
				case string:
					assert.Fail(t, "__provider was not a secret")
				case map[string]interface{}:
					assert.Equal(t, resource.SecretSig, v[resource.SigKey])
				}
			}
			// Ensure the resulting output had the expected value
			code, ok := stackInfo.Outputs["out"].(string)
			assert.True(t, ok)
			assert.Equal(t, "200", code)
		},
	})
}

//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestPartialValuesPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("partial_values", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		AllowEmptyPreviewChanges: true,
	})
}

// Tests a resource with a large (>4mb) string prop in Python
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestLargeResourcePython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("large_resource", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
	})
}

// Test enum outputs
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestEnumOutputsPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("enums", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			assert.NotNil(t, stack.Outputs)

			assert.Equal(t, "Burgundy", stack.Outputs["myTreeType"])
			assert.Equal(t, "Pulumi Planters Inc.foo", stack.Outputs["myTreeFarmChanged"])
			assert.Equal(t, "My Burgundy Rubber tree is from Pulumi Planters Inc.", stack.Outputs["mySentence"])
		},
	})
}

// Test to ensure Pylint is clean.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestPythonPylint(t *testing.T) {
	t.Skip("Temporarily skipping test - pulumi/pulumi#4849")
	var opts *integration.ProgramTestOptions
	opts = &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "pylint"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			randomURN := stack.Outputs["random_urn"].(string)
			assert.NotEmpty(t, randomURN)

			randomID := stack.Outputs["random_id"].(string)
			randomVal := stack.Outputs["random_val"].(string)
			assert.Equal(t, randomID, randomVal)

			cwd := stack.Outputs["cwd"].(string)
			assert.NotEmpty(t, cwd)

			pylint := filepath.Join("venv", "bin", "pylint")
			if runtime.GOOS == WindowsOS {
				pylint = filepath.Join("venv", "Scripts", "pylint")
			}

			err := integration.RunCommand(t, "pylint", []string{pylint, "__main__.py"}, cwd, opts)
			assert.NoError(t, err)
		},
		Quick: true,
	}
	integration.ProgramTest(t, opts)
}

// Test Python SDK codegen to ensure <Resource>Args and traditional keyword args work.
func TestPythonResourceArgs(t *testing.T) {
	t.Parallel()

	testdir := filepath.Join("python", "resource_args")

	// Generate example library from schema.
	schemaBytes, err := os.ReadFile(filepath.Join(testdir, "schema.json"))
	assert.NoError(t, err)
	var spec schema.PackageSpec
	assert.NoError(t, json.Unmarshal(schemaBytes, &spec))
	pkg, err := schema.ImportSpec(spec, nil)
	assert.NoError(t, err)
	files, err := pygen.GeneratePackage("test", pkg, map[string][]byte{})
	assert.NoError(t, err)
	outdir := filepath.Join(testdir, "lib")
	assert.NoError(t, os.RemoveAll(outdir))
	for f, contents := range files {
		outfile := filepath.Join(outdir, f)
		assert.NoError(t, os.MkdirAll(filepath.Dir(outfile), 0o755))
		assert.NoError(t, os.WriteFile(outfile, contents, 0o600))
	}
	assert.NoError(t, os.WriteFile(filepath.Join(outdir, "README.md"), []byte(""), 0o600))

	// Test the program.
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: testdir,
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
			filepath.Join(testdir, "lib"),
		},
		Quick:      true,
		NoParallel: true,
	})
}

// Test to ensure that internal stacks are hidden
func TestPythonStackTruncate(t *testing.T) {
	t.Parallel()

	cases := []string{
		"normal",
		"main_specified",
		"main_dir_specified",
	}

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	for _, name := range cases {
		// Test the program.
		t.Run(name, func(t *testing.T) {
			integration.ProgramTest(t, &integration.ProgramTestOptions{
				Dir: filepath.Join("python", "stack_truncate", name),
				Dependencies: []string{
					filepath.Join("..", "..", "sdk", "python", "env", "src"),
				},
				Quick: true,
				// This test should fail because it raises an exception
				ExpectFailure: true,
				// We need to validate that the failure has a truncated stack trace
				ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
					// Ensure that we have a non-empty list of events.
					assert.NotEmpty(t, stackInfo.Events)

					const stacktraceLinePrefix = "  File "

					// get last DiagnosticEvent containing python stack trace
					stackTraceMessage := ""
					for _, e := range stackInfo.Events {
						if e.DiagnosticEvent == nil {
							continue
						}
						msg := e.DiagnosticEvent.Message
						if !strings.Contains(msg, "Traceback") {
							continue
						}
						if !strings.Contains(msg, stacktraceLinePrefix) {
							continue
						}
						stackTraceMessage = msg
					}
					assert.NotEqual(t, "", stackTraceMessage)

					// make sure that the stack trace contains 2 frames as per the
					// program we're running
					numStackTraces := strings.Count(stackTraceMessage, stacktraceLinePrefix)
					assert.Equal(t, 2, numStackTraces)
				},
			})
		})
	}
}

// Test remote component construction with a child resource that takes a long time to be created, ensuring it's created.
func TestConstructSlowPython(t *testing.T) {
	t.Parallel()

	localProvider := testComponentSlowLocalProvider(t)

	// TODO[pulumi/pulumi#5455]: Dynamic providers fail to load when used from multi-lang components.
	// Until we've addressed this, set PULUMI_TEST_YARN_LINK_PULUMI, which tells the integration test
	// module to run `yarn install && yarn link @pulumi/pulumi` in the Python program's directory, allowing
	// the Node.js dynamic provider plugin to load.
	// When the underlying issue has been fixed, the use of this environment variable inside the integration
	// test module should be removed.
	const testYarnLinkPulumiEnv = "PULUMI_TEST_YARN_LINK_PULUMI=true"

	testDir := "construct_component_slow"
	runComponentSetup(t, testDir)

	opts := &integration.ProgramTestOptions{
		Env: []string{testYarnLinkPulumiEnv},
		Dir: filepath.Join(testDir, "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: []integration.LocalDependency{localProvider},
		Quick:          true,
		NoParallel:     true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.NotNil(t, stackInfo.Deployment)
			if assert.Equal(t, 5, len(stackInfo.Deployment.Resources)) {
				stackRes := stackInfo.Deployment.Resources[0]
				assert.NotNil(t, stackRes)
				assert.Equal(t, resource.RootStackType, stackRes.Type)
				assert.Equal(t, "", string(stackRes.Parent))
			}
		},
	}
	integration.ProgramTest(t, opts)
}

// Test remote component construction with prompt inputs.
func TestConstructPlainPython(t *testing.T) {
	t.Parallel()

	testDir := "construct_component_plain"
	runComponentSetup(t, testDir)

	tests := []struct {
		componentDir          string
		expectedResourceCount int
		env                   []string
	}{
		{
			componentDir:          "testcomponent",
			expectedResourceCount: 9,
			// TODO[pulumi/pulumi#5455]: Dynamic providers fail to load when used from multi-lang components.
			// Until we've addressed this, set PULUMI_TEST_YARN_LINK_PULUMI, which tells the integration test
			// module to run `yarn install && yarn link @pulumi/pulumi` in the Go program's directory, allowing
			// the Node.js dynamic provider plugin to load.
			// When the underlying issue has been fixed, the use of this environment variable inside the integration
			// test module should be removed.
			env: []string{"PULUMI_TEST_YARN_LINK_PULUMI=true"},
		},
		{
			componentDir:          "testcomponent-python",
			expectedResourceCount: 9,
		},
		{
			componentDir:          "testcomponent-go",
			expectedResourceCount: 8, // One less because no dynamic provider.
		},
	}

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	for _, test := range tests {
		test := test
		t.Run(test.componentDir, func(t *testing.T) {
			localProviders := []integration.LocalDependency{
				{Package: "testcomponent", Path: filepath.Join(testDir, test.componentDir)},
			}
			integration.ProgramTest(t,
				optsForConstructPlainPython(t, test.expectedResourceCount, localProviders, test.env...))
		})
	}
}

func optsForConstructPlainPython(t *testing.T, expectedResourceCount int, localProviders []integration.LocalDependency,
	env ...string,
) *integration.ProgramTestOptions {
	return &integration.ProgramTestOptions{
		Env: env,
		Dir: filepath.Join("construct_component_plain", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: localProviders,
		Quick:          true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			assert.NotNil(t, stackInfo.Deployment)
			assert.Equal(t, expectedResourceCount, len(stackInfo.Deployment.Resources))
		},
	}
}

// Test remote component inputs properly handle unknowns.
func TestConstructUnknownPython(t *testing.T) {
	t.Parallel()
	testConstructUnknown(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

// Test methods on remote components.
func TestConstructMethodsPython(t *testing.T) {
	t.Parallel()

	testDir := "construct_component_methods"
	runComponentSetup(t, testDir)

	tests := []struct {
		componentDir string
	}{
		{
			componentDir: "testcomponent",
		},
		{
			componentDir: "testcomponent-python",
		},
		{
			componentDir: "testcomponent-go",
		},
	}

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	for _, test := range tests {
		test := test
		t.Run(test.componentDir, func(t *testing.T) {
			localProvider := integration.LocalDependency{
				Package: "testcomponent", Path: filepath.Join(testDir, test.componentDir),
			}
			integration.ProgramTest(t, &integration.ProgramTestOptions{
				Dir: filepath.Join(testDir, "python"),
				Dependencies: []string{
					filepath.Join("..", "..", "sdk", "python", "env", "src"),
				},
				LocalProviders: []integration.LocalDependency{localProvider},
				Quick:          true,
				ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
					assert.Equal(t, "Hello World, Alice!", stackInfo.Outputs["message"])
				},
			})
		})
	}
}

func findResource(token string, resources []apitype.ResourceV3) *apitype.ResourceV3 {
	for _, r := range resources {
		if string(r.Type) == token {
			return &r
		}
	}
	return nil
}

//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestConstructComponentWithIdOutputPython(t *testing.T) {
	testDir := "construct_component_id_output"

	// the component implementation is written as a simple provider in go
	localProvider := integration.LocalDependency{
		Package: "testcomponent", Path: filepath.Join(testDir, "testcomponent-go"),
	}

	// run python program against the component
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join(testDir, "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: []integration.LocalDependency{localProvider},
		Quick:          true,
		ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
			component := findResource("testcomponent:index:Component", stackInfo.Deployment.Resources)
			require.NotNil(t, component, "component should be present in the deployment")
			require.NotNil(t, component.Outputs, "component should have outputs")
			componentID, ok := component.Outputs["id"].(string)
			require.True(t, ok, "component should have an output called ID")
			require.Equal(t, "42-hello", componentID, "component id output should be '42-hello'")

			// the stack should also have an output called ID
			stack := findResource("pulumi:pulumi:Stack", stackInfo.Deployment.Resources)
			require.NotNil(t, stack, "stack should be present in the deployment")
			require.NotNil(t, stack.Outputs, "stack should have outputs")
			stackID, ok := stack.Outputs["id"].(string)
			require.True(t, ok, "stack should have an output named 'id'")
			require.Equal(t, "42-hello", stackID, "stack id output should be '42-hello'")
		},
	})
}

func TestConstructMethodsUnknownPython(t *testing.T) {
	t.Parallel()
	testConstructMethodsUnknown(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

func TestConstructMethodsResourcesPython(t *testing.T) {
	t.Parallel()
	testConstructMethodsResources(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

func TestConstructMethodsErrorsPython(t *testing.T) {
	t.Parallel()
	testConstructMethodsErrors(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

func TestConstructMethodsProviderPython(t *testing.T) {
	t.Parallel()
	testConstructMethodsProvider(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

func TestConstructProviderPython(t *testing.T) {
	t.Parallel()

	const testDir = "construct_component_provider"
	runComponentSetup(t, testDir)

	tests := []struct {
		componentDir string
	}{
		{
			componentDir: "testcomponent",
		},
		{
			componentDir: "testcomponent-python",
		},
		{
			componentDir: "testcomponent-go",
		},
	}

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	for _, test := range tests {
		test := test
		t.Run(test.componentDir, func(t *testing.T) {
			localProvider := integration.LocalDependency{
				Package: "testcomponent", Path: filepath.Join(testDir, test.componentDir),
			}
			integration.ProgramTest(t, &integration.ProgramTestOptions{
				Dir: filepath.Join(testDir, "python"),
				Dependencies: []string{
					filepath.Join("..", "..", "sdk", "python", "env", "src"),
				},
				LocalProviders: []integration.LocalDependency{localProvider},
				Quick:          true,
				ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
					assert.Equal(t, "hello world", stackInfo.Outputs["message"])
				},
			})
		})
	}
}

//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestGetResourcePython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("get_resource", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		AllowEmptyPreviewChanges: true,
	})
}

func TestPythonAwaitOutputs(t *testing.T) {
	t.Parallel()

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("SuccessSimple", func(t *testing.T) {
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "success"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			Quick:                    true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				sawMagicStringMessage := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "magic string") {
							sawMagicStringMessage = true
						}
					}
				}
				assert.True(t, sawMagicStringMessage, "Did not see printed message from unexported output")
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("SuccessMultipleOutputs", func(t *testing.T) {
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "multiple_outputs"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			Quick:                    true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				sawMagicString := false
				sawFoo := false
				sawBar := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "magic string") {
							sawMagicString = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "bar") {
							sawBar = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "foo") {
							sawFoo = true
						}
					}
				}
				msg := "Did not see printed message from unexported output"
				assert.True(t, sawMagicString, msg)
				assert.True(t, sawFoo, msg)
				assert.True(t, sawBar, msg)
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("CreateWithinApply", func(t *testing.T) {
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "create_inside_apply"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			Quick:                    true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				sawUrn := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "pulumi-python:dynamic:Resource::magic_string") {
							sawUrn = true
						}
					}
				}
				assert.True(t, sawUrn)
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("ErrorHandlingSuccess", func(t *testing.T) {
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "error_handling"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			Quick:                    true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				sawMagicStringMessage := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "oh yeah") {
							sawMagicStringMessage = true
						}
					}
				}
				assert.True(t, sawMagicStringMessage, "Did not see printed message from unexported output")
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("FailureSimple", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		expectedError := "IndexError: list index out of range"
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "failure"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			ExpectFailure:            true,
			Quick:                    true,
			Stderr:                   stderr,
			ExtraRuntimeValidation: func(t *testing.T, stackInfo integration.RuntimeValidationStackInfo) {
				output := stderr.String()
				assert.Contains(t, output, expectedError)
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("FailureWithExportedOutput", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		expectedError := "IndexError: list index out of range"
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "failure_exported_output"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			ExpectFailure:            true,
			Quick:                    true,
			Stderr:                   stderr,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				output := stderr.String()
				assert.Contains(t, output, expectedError)
				sawFoo := false
				sawPrinted := false
				sawNotPrinted := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "not printed") {
							sawNotPrinted = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "printed") {
							sawPrinted = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "foo") {
							sawFoo = true
						}
					}
				}
				assert.True(t, sawPrinted)
				assert.True(t, sawFoo)
				assert.False(t, sawNotPrinted)
			},
		})
	})

	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("FailureMultipleOutputs", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		expectedError := "IndexError: list index out of range"
		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "failure_multiple_unexported_outputs"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			AllowEmptyPreviewChanges: true,
			ExpectFailure:            true,
			Quick:                    true,
			Stderr:                   stderr,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				output := stderr.String()
				assert.Contains(t, output, expectedError)
				sawFoo := false
				sawPrinted := false
				sawNotPrinted := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "not printed") {
							sawNotPrinted = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "printed") {
							sawPrinted = true
						}
						if strings.Contains(evt.DiagnosticEvent.Message, "foo") {
							sawFoo = true
						}
					}
				}
				assert.True(t, sawPrinted)
				assert.True(t, sawFoo)
				assert.False(t, sawNotPrinted)
			},
		})
	})

	// This checks we await outputs but not asyncio.tasks
	//
	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("AsyncioTasks", func(t *testing.T) {
		version, err := exec.Command("python3", "--version").Output()
		require.NoError(t, err)
		if strings.Contains(string(version), "3.8") {
			t.Skip("Skipping test as Python version is < 3.9 and asyncio.to_thread is only available in 3.9+")
		}

		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "asyncio_tasks"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			Quick: true,
			ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
				sawMagicStringMessage := false
				for _, evt := range stack.Events {
					if evt.DiagnosticEvent != nil {
						if strings.Contains(evt.DiagnosticEvent.Message, "PRINT: 42") {
							sawMagicStringMessage = true
						}
					}
				}
				assert.True(t, sawMagicStringMessage, "Did not see printed message from unexported output")
			},
		})
	})

	// This checks we don't leak futures awaiting outputs. Regression test for
	// https://github.com/pulumi/pulumi/issues/16055.
	//
	//nolint:paralleltest // ProgramTest calls t.Parallel()
	t.Run("OutputLeak", func(t *testing.T) {
		version, err := exec.Command("python3", "--version").Output()
		require.NoError(t, err)
		if strings.Contains(string(version), "3.8") {
			t.Skip("Skipping test as Python version is < 3.9 and asyncio.to_thread is only available in 3.9+")
		}

		integration.ProgramTest(t, &integration.ProgramTestOptions{
			Dir: filepath.Join("python_await", "output_leak"),
			Dependencies: []string{
				filepath.Join("..", "..", "sdk", "python", "env", "src"),
			},
			Quick:   true,
			Verbose: true,
		})
	})
}

// Test dict key translations.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestPythonTranslation(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "translation"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
	})
}

func TestComponentProviderSchemaPython(t *testing.T) {
	t.Parallel()
	// TODO[https://github.com/pulumi/pulumi/issues/12365] we no longer have shim files so there's no native
	// binary for the testComponentProviderSchema to just exec. It _ought_ to be rewritten to use the plugin
	// host framework so that it starts the component up the same as all the other tests are doing (via
	// shimless).
	t.Skip("testComponentProviderSchema needs to be updated to use a plugin host to deal with non-native-binary providers")

	path := filepath.Join("component_provider_schema", "testcomponent-python", "pulumi-resource-testcomponent")
	if runtime.GOOS == WindowsOS {
		path += ".cmd"
	}
	testComponentProviderSchema(t, path)
}

// Test that the about command works as expected. Because about parses the
// results of each runtime independently, we have an integration test in each
// language.
func TestAboutPython(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("about", "python")

	e := ptesting.NewEnvironment(t)
	defer e.DeleteIfNotFailed()
	e.ImportDirectory(dir)

	stdout, _ := e.RunCommand("pulumi", "about", "--json")
	// Assert we parsed the dependencies
	assert.Contains(t, stdout, "pulumi-kubernetes")
}

func TestConstructOutputValuesPython(t *testing.T) {
	t.Parallel()
	testConstructOutputValues(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

// TestResourceRefsGetResourcePython tests that invoking the built-in 'pulumi:pulumi:getResource' function
// returns resource references for any resource reference in a resource's state.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestResourceRefsGetResourcePython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("resource_refs_get_resource", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
	})
}

// TestDeletedWithPython tests the DeletedWith resource option.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestDeletedWithPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("deleted_with", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: []integration.LocalDependency{
			{Package: "testprovider", Path: filepath.Join("..", "testprovider")},
		},
		Quick: true,
	})
}

func TestConstructProviderPropagationPython(t *testing.T) {
	t.Parallel()

	testConstructProviderPropagation(t, "python", []string{
		filepath.Join("..", "..", "sdk", "python", "env", "src"),
	})
}

// Regression test for https://github.com/pulumi/pulumi/issues/9411
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestDuplicateOutputPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "duplicate-output"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			expected := []interface{}{float64(1), float64(2)}
			assert.Equal(t, expected, stack.Outputs["export1"])
			assert.Equal(t, expected, stack.Outputs["export2"])
		},
	})
}

func TestConstructProviderExplicitPython(t *testing.T) {
	t.Parallel()

	testConstructProviderExplicit(t, "python", []string{
		filepath.Join("..", "..", "sdk", "python", "env", "src"),
	})
}

// Regression test for https://github.com/pulumi/pulumi/issues/13551
//
//nolint:paralleltest // ProgramTestManualLifeCycle calls t.Parallel()
func TestFailsOnImplicitDependencyCyclesPython(t *testing.T) {
	t.Skip("Temporarily skipping flakey test - pulumi/pulumi#14708")

	stdout := &bytes.Buffer{}
	pt := integration.ProgramTestManualLifeCycle(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "implicit-dependency-cycles"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Stdout: stdout,
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			assert.Contains(
				t, stdout.String(),
				"RuntimeError: We have detected a circular dependency involving a resource of type "+
					"my:module:Child-1 named a-child-1.")
			assert.Contains(
				t, stdout.String(),
				"Please review any `depends_on`, `parent` or other dependency relationships between "+
					"your resources to ensure no cycles have been introduced in your program.")
		},
	})
	require.NoError(t, pt.TestLifeCyclePrepare(), "prepare")
	t.Cleanup(pt.TestCleanUp)

	require.NoError(t, pt.TestLifeCycleInitialize(), "initialize")
	require.Error(t, pt.TestPreviewUpdateAndEdits(), "preview")
}

// Test a paramaterized provider with python.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestParameterizedPython(t *testing.T) {
	// TODO: Unskip this test after 3.134.0 is released. Python codegen for parameterized providers will try to
	// refer to release 3.134.0, but until we actually release that version pip will fail that this constraint
	// can't be met.
	t.Skip("This needs to skip until 3.134.0 is released due to how pip resoloution works")

	e := ptesting.NewEnvironment(t)

	// We can't use ImportDirectory here because we need to run this in the right directory such that the relative paths
	// work. This also means we don't delete the directory after the test runs.
	var err error
	e.CWD, err = filepath.Abs("python/parameterized")
	require.NoError(t, err)

	err = os.RemoveAll(filepath.Join("python", "parameterized", "sdk"))
	require.NoError(t, err)

	_, _ = e.RunCommand("pulumi", "package", "gen-sdk", "../../../testprovider", "pkg", "--language", "python", "--local")

	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("python", "parameterized"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		LocalProviders: []integration.LocalDependency{
			{Package: "testprovider", Path: filepath.Join("..", "testprovider")},
		},
	})
}

func TestConfigGetterOverloads(t *testing.T) {
	t.Parallel()
	e := ptesting.NewEnvironment(t)
	defer e.DeleteIfNotFailed()
	e.ImportDirectory("python/config-getter-types")

	stackName := ptesting.RandomStackName()
	e.RunCommand("pulumi", "install")
	e.RunCommand("pulumi", "login", "--cloud-url", e.LocalURL())
	e.RunCommand("pulumi", "stack", "init", stackName)
	defer e.RunCommand("pulumi", "stack", "rm", "--yes", "--stack", stackName)

	// ProgramTest installs extra dependencies as editable packages using the `-e` flag, but typecheckers do not
	// handle editable packages well. We have to manually install the SDK without `-e` flag instead.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	sdkPath := filepath.Join(cwd, "..", "..", "sdk", "python", "env", "src")
	pythonBin := "./venv/bin/python"
	if runtime.GOOS == "windows" {
		pythonBin = ".\\venv\\Scripts\\python.exe"
	}
	e.RunCommand(pythonBin, "-m", "pip", "install", sdkPath)

	// Add some config values
	e.RunCommand("pulumi", "config", "set", "foo", "bar")
	e.RunCommand("pulumi", "config", "set", "foo_int", "42")
	e.RunCommand("pulumi", "config", "set", "--secret", "foo_secret", "3")

	// Run a preview. This will typecheck the program and fail if typechecking has errors.
	e.RunCommand("pulumi", "preview")
}

// Test that we can run a program, attach a debugger to it, and send debugging commands using the dap protocol
// and finally that the program terminates successfully after the debugger is detached.
func TestDebuggerAttachPython(t *testing.T) {
	t.Parallel()

	e := ptesting.NewEnvironment(t)
	defer e.DeleteIfNotFailed()
	e.ImportDirectory(filepath.Join("python", "venv"))

	e.RunCommand("pulumi", "login", "--cloud-url", e.LocalURL())

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.Env = append(e.Env, "PULUMI_DEBUG_COMMANDS=true")
		e.RunCommand("pulumi", "stack", "init", "debugger-test")
		e.RunCommand("pulumi", "stack", "select", "debugger-test")
		e.RunCommand("pulumi", "preview", "--attach-debugger",
			"--event-log", filepath.Join(e.RootPath, "debugger.log"))
	}()

	// Wait for the debugging event
	wait := 20 * time.Millisecond
	var debugEvent *apitype.StartDebuggingEvent
outer:
	for i := 0; i < 50; i++ {
		events, err := readUpdateEventLog(filepath.Join(e.RootPath, "debugger.log"))
		require.NoError(t, err)
		for _, event := range events {
			if event.StartDebuggingEvent != nil {
				debugEvent = event.StartDebuggingEvent
				break outer
			}
		}
		time.Sleep(wait)
		wait *= 2
	}
	require.NotNil(t, debugEvent)

	// We've attached a debugger, so we need to connect to it and let the program continue.
	conn, err := net.Dial("tcp", "localhost:"+strconv.Itoa(
		int(debugEvent.Config["connect"].(map[string]interface{})["port"].(float64))))
	if err != nil {
		log.Fatalf("Failed to connect to debugger: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	resp, err := dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.OutputEvent{}, resp)
	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.OutputEvent{}, resp)
	resp, err = dap.ReadProtocolMessage(reader)
	// go-dap doesn't support this event, but we need to read it
	// anyway.  We don't actually care that it's not supported,
	// since we don't want to do anyting with it.
	require.ErrorContains(t, err, "Event event 'debugpySockets' is not supported (seq: 3)")
	require.Nil(t, resp)

	seq := 0
	err = dap.WriteProtocolMessage(conn, &dap.InitializeRequest{
		Request: newDAPRequest(seq, "initialize"),
		Arguments: dap.InitializeRequestArguments{
			ClientID:        "pulumi",
			ClientName:      "Pulumi",
			AdapterID:       "pulumi",
			Locale:          "en-us",
			LinesStartAt1:   true,
			ColumnsStartAt1: true,
		},
	})
	require.NoError(t, err)
	seq++

	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.InitializeResponse{}, resp)

	json, err := json.Marshal(debugEvent.Config)
	require.NoError(t, err)
	err = dap.WriteProtocolMessage(conn, &dap.AttachRequest{
		Request:   newDAPRequest(seq, "attach"),
		Arguments: json,
	})
	require.NoError(t, err)
	seq++

	resp, err = dap.ReadProtocolMessage(reader)
	// As above we don't care about the details of this event
	require.ErrorContains(t, err, "Event event 'debugpyWaitingForServer' is not supported (seq: 5)")
	require.Nil(t, resp)

	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.InitializedEvent{}, resp)

	err = dap.WriteProtocolMessage(conn, &dap.ConfigurationDoneRequest{
		Request: newDAPRequest(seq, "configurationDone"),
	})
	require.NoError(t, err)

	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.ConfigurationDoneResponse{}, resp)

	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.AttachResponse{}, resp)

	resp, err = dap.ReadProtocolMessage(reader)
	require.NoError(t, err)
	require.IsType(t, &dap.ProcessEvent{}, resp)

	for {
		resp, err = dap.ReadProtocolMessage(reader)
		require.NoError(t, err)
		if reflect.TypeOf(resp) == reflect.TypeOf(&dap.TerminatedEvent{}) {
			break
		}
		require.IsType(t, &dap.ThreadEvent{}, resp)
	}
	conn.Close()

	// Make sure the program finished successfully.
	wg.Wait()
}

func TestConstructFailuresPython(t *testing.T) {
	t.Parallel()
	testConstructFailures(t, "python", filepath.Join("..", "..", "sdk", "python", "env", "src"))
}

// TestLogDebugPython tests that the amount of debug logs is reasonable.
//
//nolint:paralleltest // ProgramTest calls t.Parallel()
func TestLogDebugPython(t *testing.T) {
	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Dir: filepath.Join("log_debug", "python"),
		Dependencies: []string{
			filepath.Join("..", "..", "sdk", "python", "env", "src"),
		},
		Quick: true,
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			var count int
			for _, ev := range stack.Events {
				if de := ev.DiagnosticEvent; de != nil && de.Severity == "debug" {
					count++
				}
			}
			t.Logf("Found %v debug log events", count)

			// Ensure at least 1 debug log events are emitted, confirming debug logs are working as expected.
			assert.Greaterf(t, count, 0, "%v is not enough debug log events", count)

			// More than 25 debug log events on such a simple program is very likely unintended.
			assert.LessOrEqual(t, count, 25, "%v is too many debug log events", count)
		},
	})
}
