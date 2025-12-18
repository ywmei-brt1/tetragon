# Feature Request: Support for `int32_arr` argument type

## Add a title
Support for `int32_arr` argument type to capture integer array output parameters

## Is your feature request related to a problem?
Yes. Currently, Tetragon lacks the ability to natively capture arguments that are pointers to arrays of integers, specifically when those arrays are used as output parameters populated by the kernel. 

Standard examples include:

1. `pipe2(int pipefd[2], int flags)`: The first argument, `pipefd`, is a pointer to an array of two integers.
2. `socketpair(int domain, int type, int protocol, int sv[2])`: The fourth argument, `sv`, is a pointer to an array of two integers.

In both cases, the kernel populates these arrays with new file descriptors during syscall execution. Without support for an `int32_arr` type, users cannot observe the actual file descriptors returned by these syscalls, creating a gap in visibility regarding process inter-communication channels and resource allocation.

## Describe the feature you would like
I would like to introduce a new argument type `int32_arr` to Tetragon's TracingPolicy configuration. This feature should:

1.  **Support Array Types**: Allow `TracingPolicy` arguments to be defined with `type: "int32_arr"` and a `size` field (requiring explicit definition).
2.  **Support Return Copy**: Fully support `returnCopy: true` for this type. Since these arrays are often output parameters (like in `pipe2`), the BPF collector must be able to defer reading the memory until the function returns (kretprobe) to capture the values populated by the kernel.
3.  **API Visibility**: Expose these values in the Tetragon API (gRPC and JSON export) as a structured list of integers, rather than raw byte buffers.

## Describe your proposed solution
I have implemented a solution that extends the existing argument parsing logic:

1.  **BPF Kernel Side**:
    - Introduced `GenericInt32ArrType` to the BPF sensor.
    - Leveraged the existing `returnCopy` mechanism. When used, the BPF program emits a specific sentinel, `CHAR_BUF_SAVED_FOR_RETPROBE` (`0xFFFFFFFC`), during the entry probe (kprobe). This signals the usage of the existing "submit later" infrastructure to capture the array data from the user-provided pointer once the syscall returns.
    - **Dynamic Sizing**: Extended the `TracingPolicy` argument definition to include a `size` field. This allows the BPF program to dynamically read the specified number of elements, supporting arrays of varying lengths beyond just `pipe2` and `socketpair`.
    
2.  **Protobuf API**:
    - Updated `tetragon.proto` to include a new `Int32ListArg` field within the `KprobeArgument` message, ensuring type-safe transport of the integer list.

3.  **Go Userspace (Observer)**:
    - Updated `pkg/sensors/tracing/args_linux.go` to handle `GenericInt32ArrType`.
    - The parser recognizes the `CHAR_BUF_SAVED_FOR_RETPROBE` sentinel and correctly reassembles the `int32` slice from the return event data.
    
4.  **Validation**:
    - Added an end-to-end test, `TestKprobePipe2Return`, which traces the `pipe2` syscall, captures the `int32_arr` output, and verifies that the reported file descriptors match the actual file descriptors allocated by the OS.
