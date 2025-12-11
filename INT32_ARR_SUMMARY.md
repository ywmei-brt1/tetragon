# Tetragon int32_arr Support Implementation Summary

## Purpose
The goal is to extend Tetragon's API and logic to support collecting an array of `int32` as a return value from kprobes. This is specifically required for capturing the file descriptors returned by `pipe2(int pipefd[2], int flags)`.

## Current Status
**Step 1: API Definition** is complete.
The Protocol Buffers definition, Kubernetes CRD validation, and internal Go type mappings have been updated to recognize a new type `int32_arr`.

## Next Steps
1.  **C Code Implementation (BPF)**:
    *   Update BPF C code to handle the `GenericInt32ArrType` (value 43).
    *   Implement logic to read the array (currently hardcoded to 2 integers for `pipe2` use case) from the pointer provided in the arguments.
    *   Populate the `KprobeInt32List` structure in the perf event output.
2.  **Golang Code Implementation**:
    *   Update the userspace event processing logic to parse the `int32_arr` event data received from BPF.
    *   Format it correctly for the JSON/gRPC output.

## Modified Files

### 1. `api/v1/tetragon/tetragon.proto`
*   **Change**: Added `message KprobeInt32List` and added `KprobeInt32List int32_list_arg = 31;` to the `KprobeArgument` `oneof`.
*   **Reason**: Defines the data structure for transmitting the array of integers from the agent to the client (Hubble/CLI).

### 2. `pkg/k8s/apis/cilium.io/v1alpha1/types.go`
*   **Change**: Added `int32_arr` to the `// +kubebuilder:validation:Enum` list for `KProbeArg.Type`.
*   **Reason**: Allows users to specify `type: "int32_arr"` in their TracingPolicy YAML files.

### 3. `pkg/generictypes/generictypes.go`
*   **Change**:
    *   Added `GenericInt32ArrType = 43` constant.
    *   Updated `genericStringToType` and `genericTypeToStringTable` maps.
*   **Reason**: Maps the string representation `"int32_arr"` to an internal integer constant used by the Go agent and BPF code interaction.

## Generation Commands
If you rebase or modify the `.proto` / `.go` files again, you **must** regenerate the code.

**IMPORTANT**: The standard `make codegen` might fail due to "breaking changes" checks in the proto definitions. Use the following sequence to bypass strictly non-breaking checks if necessary:

```bash
# 1. Regenerate Go bindings from Proto (skipping breaking change check)
make -C api proto

# 2. Regenerate Kubernetes CRDs and deepcopy methods
make generate
```

## Verification Commands
To ensure the API changes are valid and compilation is safe:

```bash
# 1. Validate KProbe Argument types (checks if int32_arr is accepted)
go test ./pkg/sensors/tracing -run TestKprobeValidation

# 2. Compile all packages to ensure no type mismatches
make test-compile
```
