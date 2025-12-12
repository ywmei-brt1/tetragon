// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

package tracing

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/cilium/tetragon/pkg/api/tracingapi"
	gt "github.com/cilium/tetragon/pkg/generictypes"
	"github.com/stretchr/testify/assert"
)

func TestGetArgInt32Arr(t *testing.T) {
	tests := []struct {
		name          string
		inputCount    uint32
		inputValues   []int32
		expectedVals  []int32
		expectedError bool
	}{
		{
			name:         "Normal 2 values",
			inputCount:   2,
			inputValues:  []int32{10, 20},
			expectedVals: []int32{10, 20},
		},
		{
			name:         "Single value",
			inputCount:   1,
			inputValues:  []int32{99},
			expectedVals: []int32{99},
		},
		{
			name:         "Empty array",
			inputCount:   0,
			inputValues:  []int32{},
			expectedVals: []int32{},
		},
		{
			name:        "Saved for retprobe magic value",
			inputCount:  0xFFFFFFFC, // -4 as uint32
			inputValues: []int32{},
			// Should return empty arg with no error, logical handling implies waiting for next event
			expectedVals: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct the binary buffer
			buf := new(bytes.Buffer)
			// Write count
			err := binary.Write(buf, binary.LittleEndian, tt.inputCount)
			assert.NoError(t, err)

			// Write values
			if tt.inputCount != 0xFFFFFFFC {
				err = binary.Write(buf, binary.LittleEndian, tt.inputValues)
				assert.NoError(t, err)
			}

			r := bytes.NewReader(buf.Bytes())
			argPrinter := argPrinter{
				ty:    gt.GenericInt32ArrType,
				index: 0,
				label: "test_arg",
			}

			// Call getArg
			res := getArg(r, argPrinter)

			// Assertions
			if tt.expectedVals == nil && tt.inputCount == 0xFFFFFFFC {
				// Special case: check what valid return looks like for retprobe placeholder
				// Our implementation currently returns an empty-ish struct or similar.
				// Based on implementation: returns MsgGenericKprobeArgInt32List with nil/empty Value
				typedRes, ok := res.(tracingapi.MsgGenericKprobeArgInt32List)
				assert.True(t, ok, "Expected MsgGenericKprobeArgInt32List")
				assert.Empty(t, typedRes.Value)
			} else {
				typedRes, ok := res.(tracingapi.MsgGenericKprobeArgInt32List)
				assert.True(t, ok, "Expected MsgGenericKprobeArgInt32List")
				assert.Equal(t, tt.expectedVals, typedRes.Value)
			}
		})
	}
}
