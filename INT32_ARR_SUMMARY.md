# Tetragon int32_arr Support Implementation Summary

## Purpose
The goal is to extend Tetragon's API and logic to support collecting an array of `int32` as a return value from kprobes. This is specifically required for capturing the file descriptors returned by `pipe2(int pipefd[2], int flags)`.

## Current Status
**Step 1: API Definition** is complete.
The Protocol Buffers definition, Kubernetes CRD validation, and internal Go type mappings have been updated to recognize a new type `int32_arr`.

## Phase 1: API Definition (Complete)
This phase established the contract for the new argument type in Protobuf and Kubernetes CRDs.

### Modified Files
1.  **`api/v1/tetragon/tetragon.proto`**
    *   **Change**: Added `message KprobeInt32List` and added `KprobeInt32List int32_list_arg = 31;` to the `KprobeArgument` `oneof`.
    *   **Reason**: Defines the data structure for transmitting the array of integers from the agent to the client (Hubble/CLI).
2.  **`pkg/k8s/apis/cilium.io/v1alpha1/types.go`**
    *   **Change**: Added `int32_arr` to the `// +kubebuilder:validation:Enum` list for `KProbeArg.Type`.
    *   **Reason**: Allows users to specify `type: "int32_arr"` in their TracingPolicy YAML files.
3.  **`pkg/generictypes/generictypes.go`**
    *   **Change**:
        *   Added `GenericInt32ArrType = 43` constant.
        *   Updated `genericStringToType` and `genericTypeToStringTable` maps.
    *   **Reason**: Maps the string representation `"int32_arr"` to an internal integer constant used by the Go agent and BPF code interaction.

### Generation Commands
The following commands were run to regenerate the code:
```bash
# Regenerate Go bindings from Proto (skipping breaking change check)
make -C api proto
# Regenerate Kubernetes CRDs and deepcopy methods
make generate
```

### Testing Performed
*   **Validation**: `go test ./pkg/sensors/tracing -run TestKprobeValidation` passed.
*   **Compilation**: `make test-compile` passed.

---

## Phase 2: BPF C Implementation (Complete)
This phase implemented the kernel-side logic to read the new argument type and write it to the ring buffer.

### Modified Files
1.  **`bpf/process/types/basic.h`**
    *   **Change**: Added `int32_arr_type = 43` to the internal BPF enum.
    *   **Reason**: Reserves the type ID 43 for use within the BPF logic.
2.  **`bpf/process/generic_calls.h`**
    *   **Change**: Implemented the logic for `case int32_arr_type:` in the `read_arg` function.
    *   **Rationale**:
        *   **ReturnCopy Support**: `pipe2` returns file descriptors via an out-parameter. We MUST capture the pointer address at entry (kprobe) and read the data at exit (kretprobe). I added logic to check `has_return_copy(argm)` and save the `arg` (pointer) to the `retprobe_map` if true. This mirrors the behavior of `char_buf`.
        *   **Hardcoded Size**: Since `pipe2` always returns 2 integers, and we don't yet have a generic "array size" field in the `KProbeArg` CRD, I hardcoded the read logic to read 2 integers. A future improvement would be to allow configurable sizes (e.g., via `SizeArgIndex` or `ArgSize`).
        *   **Data Format**: The data written to the ring buffer is `[count (u32)][int32][int32]...`. The Go side must expect this format.

### Testing Performed
*   **Compilation**: `make tetragon-bpf` passed, confirming the C code is syntactically correct and can be built.

---

## Next Steps (Phase 3: Golang Implementation & Verification)
1.  **Golang Code Implementation**:
    *   Update the userspace event processing logic (likely in `pkg/sensors/tracing/generickprobe.go`) to parse the `int32_arr` event data received from BPF.
    *   The BPF format is: `[count(u32)][int32][int32]...`.
    *   Format it correctly for the JSON/gRPC output (`KprobeInt32List`).
2.  **End-to-End Testing**:
    *   Create a TracingPolicy for `pipe2` using `type: "int32_arr"` and `returnCopy: true`.
# 1. Validate KProbe Argument types (checks if int32_arr is accepted)
go test ./pkg/sensors/tracing -run TestKprobeValidation

# 2. Compile all packages to ensure no type mismatches
make test-compile

# 3. Verify BPF Compilation
# This ensures the new C code syntax is correct and can be compiled by clang.
make tetragon-bpf
```
