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

## Phase 3: Golang Implementation & Verification (Complete)
This phase updated the Tetragon agent (Go userspace) to parse the events emitted by the BPF code and translate them into the Protobuf format for clients.

### Modified Files
1.  **`pkg/api/tracingapi/client_kprobe.go`**
    *   **Change**: Added `MsgGenericKprobeArgInt32List` struct to represent the internal Go version of the argument.
2.  **`pkg/sensors/tracing/args_linux.go`**
    *   **Change**: Added parsing logic in `getArg` for `gt.GenericInt32ArrType`. It reads the 4-byte count, handles the `0xFFFFFFFC` (saved for retprobe) code, and reads the array of `int32` values.
3.  **`pkg/grpc/tracing/tracing.go`**
    *   **Change**: Updated `getKprobeArgument` to handle `tracingapi.MsgGenericKprobeArgInt32List` and convert it to the `tetragon.KprobeArgument` protobuf message using `KprobeArgument_Int32ListArg`.

### Testing Performed
*   **Compilation**: `make test-compile` passed (exit code 0), verifying type safety and structural correctness of the new Go code.

---

### 5. Automated Unit Testing (Go)
Layer 2 testing (Go Parser Logic) is covered by `pkg/sensors/tracing/args_linux_test.go`.

### 6. Automated E2E Testing (Go)
Layer 1 & 3 testing (BPF + Integration) is covered by `pkg/sensors/tracing/pipe2_test.go`.

**To run the tests:**
```bash
# Run Unit Test (Parser)
go test -v ./pkg/sensors/tracing/ -run TestGetArgInt32Arr

# Run E2E Test (Requires Root & Clean Env)
sudo -E $(which go) test -v ./pkg/sensors/tracing/ -run TestKprobePipe2Return
```

## Comprehensive Testing Strategy
To ensure the `int32_arr` feature is production-ready, we adopt a three-layer testing approach.

### Layer 1 & 3: BPF and End-to-End (E2E) Integration
**Why Combine Them?**
Testing BPF code in isolation (unit testing) is complex and often requires mocking kernel infrastructure. In Tetragon, E2E tests in `pkg/sensors/tracing` are the standard for validating BPF logic. If the E2E test passes, it proves:
*   The BPF code loaded successfully.
*   The BPF code correctly read the data from the kernel (simulating Layer 1).
*   The BPF code correctly wrote the data to the ring buffer.
*   The entire pipeline delivered the event to user space.

**Implementation**: A new E2E test `TestKprobePipe2Return` (in `pkg/sensors/tracing/pipe2_test.go`) will:
1.  Load the `pipe2` TracingPolicy.
2.  Execute `unix.Pipe2()` to generate real kernel events.
3.  Verify the event contains the exact file descriptors returned by the syscall.

### Layer 2: Golang Unit Test (Parser Logic)
**Goal**: Verify that the Go userspace code correctly parses raw binary data from BPF, independent of the kernel or BPF execution. This ensures that *if* BPF sends the right bytes, Go will *always* handle them correctly.

**Scope**:
*   Target Function: `getArg` in `pkg/sensors/tracing/args_linux.go`.
*   Input: A synthetic `bytes.Buffer` simulating the BPF ring buffer format:
    *   `[count (u32)][int32_1][int32_2]...` (e.g., `[2][3][4]`)
*   Assertions:
    *   Verify it returns a `MsgGenericKprobeArgInt32List`.
    *   Verify the values match the input (`[3, 4]`).
    *   Verify it handles the `0xFFFFFFFC` (saved for retprobe) magic value correctly.

---

## Manual testing
To start tetragon, run `sudo ./tetragon --bpf-lib bpf/objs --tracing-policy examples/tracingpolicy/pipe2.yaml`.
Then start tetra: `./tetra getevents --policy-names pipe2-monitoring`.
Then run some pipe command, tetra will report the evnets: `echo "foobar" | grep foo`.
