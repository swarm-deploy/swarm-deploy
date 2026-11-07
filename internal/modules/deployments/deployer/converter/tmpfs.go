package converter

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/mount"
)

func serviceTmpfsMount(value string) (mount.Mount, error) {
	target, options, _ := strings.Cut(value, ":")
	result := mount.Mount{Type: mount.TypeTmpfs, Target: target}
	if !path.IsAbs(target) {
		return result, fmt.Errorf("tmpfs: expected absolute mount target")
	}
	if options == "" {
		return result, nil
	}
	result.TmpfsOptions = &mount.TmpfsOptions{}
	for _, option := range strings.Split(options, ",") {
		key, setting, _ := strings.Cut(option, "=")
		switch key {
		case "size":
			size, err := byteSize(setting)
			if err != nil || setting == "" {
				return result, fmt.Errorf("tmpfs.size: invalid size")
			}
			result.TmpfsOptions.SizeBytes = size
		case "mode":
			mode, err := strconv.ParseUint(setting, 8, 32)
			if err != nil || mode > maxTmpfsMode {
				return result, fmt.Errorf("tmpfs.mode: invalid mode")
			}
			result.TmpfsOptions.Mode = os.FileMode(mode)
		case "ro":
			result.ReadOnly = true
		case "rw":
			result.ReadOnly = false
		default:
			return result, fmt.Errorf("tmpfs: unsupported mount option; use size, mode, ro or rw")
		}
	}
	return result, nil
}
