// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package mountinfo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	// FilesystemType names for filesystem which are used in /proc/pid/mountinfo
	FilesystemTypeBPFFS   = "bpf"
	FilesystemTypeCgroup2 = "cgroup2"
	FilesystemTypeDebugFS = "debugfs"

	mountInfoFilepath = "/proc/self/mountinfo"
)

// MountInfo is a struct representing information from /proc/pid/mountinfo. More
// information about file syntax:
// https://www.kernel.org/doc/Documentation/filesystems/proc.txt
type MountInfo struct {
	MountID        int64
	ParentID       int64
	StDev          string
	Root           string
	MountPoint     string
	MountOptions   string
	OptionalFields []string
	FilesystemType string
	MountSource    string
	SuperOptions   string
}

// parseMountInfoFile returns a slice of *MountInfo with information parsed from
// the given reader
func parseMountInfoFile(r io.Reader) ([]*MountInfo, error) {
	var result []*MountInfo

	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		mountInfoRaw := scanner.Text()

		// Optional fields (which are on the 7th position) are separated
		// from the rest of fields by "-" character. The number of
		// optional fields can be greater or equal to 1.
		mountInfoSeparated := strings.Split(mountInfoRaw, " - ")
		if len(mountInfoSeparated) != 2 {
			return nil, fmt.Errorf("invalid mountinfo entry which has more that one '-' separator: %s", mountInfoRaw)
		}

		// Extract fields from both sides of mountinfo
		mountInfoLeft := strings.Split(strings.TrimSpace(mountInfoSeparated[0]), " ")
		mountInfoRight := strings.Split(strings.TrimSpace(mountInfoSeparated[1]), " ")

		// Before '-' separator there should be 6 fields and unknown
		// number of optional fields
		if len(mountInfoLeft) < 6 {
			return nil, fmt.Errorf("invalid mountinfo entry: %s", mountInfoRaw)
		}
		// After '-' separator there should be 3 fields
		if len(mountInfoRight) != 3 {
			return nil, fmt.Errorf("invalid mountinfo entry: %s", mountInfoRaw)
		}

		mountID, err := strconv.ParseInt(mountInfoLeft[0], 10, 64)
		if err != nil {
			return nil, err
		}

		parentID, err := strconv.ParseInt(mountInfoLeft[1], 10, 64)
		if err != nil {
			return nil, err
		}

		// Extract optional fields, which start from 7th position
		var optionalFields []string
		for i := 6; i < len(mountInfoLeft); i++ {
			optionalFields = append(optionalFields, mountInfoLeft[i])
		}

		result = append(result, &MountInfo{
			MountID:        mountID,
			ParentID:       parentID,
			StDev:          mountInfoLeft[2],
			Root:           mountInfoLeft[3],
			MountPoint:     mountInfoLeft[4],
			MountOptions:   mountInfoLeft[5],
			OptionalFields: optionalFields,
			FilesystemType: mountInfoRight[0],
			MountSource:    mountInfoRight[1],
			SuperOptions:   mountInfoRight[2],
		})
	}

	return result, nil
}

// GetMountInfo returns a slice of *MountInfo with information parsed from
// /proc/self/mountinfo
func GetMountInfo() ([]*MountInfo, error) {
	fMounts, err := os.Open(mountInfoFilepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open mount information at %s: %w", mountInfoFilepath, err)
	}
	defer fMounts.Close()

	return parseMountInfoFile(fMounts)
}

func isMountFS(mountInfos []*MountInfo, mntType string, mapRoot string) (bool, bool) {
	var mapRootMountInfo *MountInfo

	for _, mountInfo := range mountInfos {
		if mountInfo.MountPoint == mapRoot {
			mapRootMountInfo = mountInfo
			break
		}
	}

	if mapRootMountInfo == nil {
		return false, false
	}

	if mapRootMountInfo.FilesystemType == mntType {
		return true, true
	}
	return true, false
}

// IsMountFS returns two boolean values:checks whether the current mapRoot:
// - whether the current mapRoot has any mount
// - whether that mount's filesystem is of type mntType
func IsMountFS(infos []*MountInfo, mntType string, mapRoot string) (bool, bool) {
	return isMountFS(infos, mntType, mapRoot)
}
