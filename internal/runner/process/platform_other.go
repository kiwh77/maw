//go:build !darwin

package process

import "os"

// platformSupported 为假：未验证的平台不默认继承 macOS 的锁与进程身份能力。
const platformSupported = false

func acquireLock(*os.File) error { return ErrUnsupported }

func releaseLock(*os.File) error { return nil }

func inspectProcess(int) (procInfo, error) { return procInfo{}, ErrUnsupported }
