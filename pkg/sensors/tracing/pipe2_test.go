// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

package tracing

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilium/tetragon/api/v1/tetragon"
	"github.com/cilium/tetragon/pkg/observer/observertesthelper"
	"github.com/cilium/tetragon/pkg/testutils"
	tus "github.com/cilium/tetragon/pkg/testutils/sensors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Manual JSON reading validation
func validatePipe2Event(t *testing.T, fds [2]int) {
	exportFile, err := testutils.GetExportFilename(t)
	require.NoError(t, err)

	// Open the file
	f, err := os.Open(exportFile)
	require.NoError(t, err)
	defer f.Close()

	dec := json.NewDecoder(f)
	found := false
	for dec.More() {
		var event tetragon.GetEventsResponse
		if err := dec.Decode(&event); err != nil {
			t.Logf("Decode incorrect: %v", err)
			continue
		}

		// Check if it's our ProcessKprobe
		kp := event.GetProcessKprobe()
		if kp == nil {
			continue
		}

		if !strings.HasSuffix(kp.FunctionName, "sys_pipe2") {
			continue
		}

		// Verify Args
		if len(kp.Args) < 1 {
			t.Errorf("Expected args, got none")
			continue
		}

		// Arg 0 should be Int32ListArg
		arg0 := kp.Args[0]
		listArg := arg0.GetInt32ListArg()
		if listArg == nil {
			t.Errorf("Expected Int32ListArg at index 0, got %T", arg0.Arg)
			continue
		}

		if len(listArg.Values) != 2 {
			t.Logf("Skipping event with unexpected value count: %v", listArg.Values)
			continue
		}

		if listArg.Values[0] != int32(fds[0]) || listArg.Values[1] != int32(fds[1]) {
			t.Logf("Skipping event with mismatching values: expected %v, got %v", fds, listArg.Values)
			continue
		}

		found = true
		break
	}

	require.True(t, found, "Did not find matching pipe2 event in export file")
}

func TestKprobePipe2Return(t *testing.T) {
	var doneWG, readyWG sync.WaitGroup
	defer doneWG.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), tus.Conf().CmdWaitTime)
	defer cancel()

	pidStr := strconv.Itoa(int(observertesthelper.GetMyPid()))
	t.Logf("tester pid=%s\n", pidStr)


	// Determine syscall name based on arch
	callName := "sys_pipe2"
	if runtime.GOARCH == "amd64" {
		callName = "__x64_sys_pipe2"
	} else if runtime.GOARCH == "arm64" {
		callName = "__arm64_sys_pipe2"
	}

	pipe2ConfigHook := `
apiVersion: cilium.io/v1alpha1
kind: TracingPolicy
metadata:
  name: "pipe2-test"
spec:
  kprobes:
  - call: "` + callName + `"
    syscall: true
    args:
    - index: 0
      type: "int32_arr"
      returnCopy: true
      label: "pipefd"
    - index: 1
      type: "int"
      label: "flags"
    selectors:
    - matchPIDs:
      - operator: In
        followForks: true
        values:
        - ` + pidStr

	// Create temp file for config
	tmpFile, err := os.CreateTemp(t.TempDir(), "tetragon-config-*.yaml")
	if err != nil {
		t.Fatalf("createTemp: err %s", err)
	}
	testConfigFile := tmpFile.Name()
	defer os.Remove(testConfigFile)

	if _, err := tmpFile.Write([]byte(pipe2ConfigHook)); err != nil {
		t.Fatalf("write temp file: err %s", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close temp file: err %s", err)
	}

	obs, err := observertesthelper.GetDefaultObserverWithFile(t, ctx, testConfigFile, tus.Conf().TetragonLib, observertesthelper.WithMyPid())
	if err != nil {
		t.Fatalf("GetDefaultObserverWithFile error: %s", err)
	}

	observertesthelper.LoopEvents(ctx, t, &doneWG, &readyWG, obs)
	readyWG.Wait()

	// Trigger pipe2
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		t.Fatalf("unix.Pipe2 failed: %v", err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])

	t.Logf("Expected FDs: %v", fds)

	// Allow some time for events to be flushed to file
	// LoopEvents runs in background, but we need to ensure the event is written before we read.
	// Usually there is a small delay.
	time.Sleep(5 * time.Second)

	validatePipe2Event(t, fds)
}
