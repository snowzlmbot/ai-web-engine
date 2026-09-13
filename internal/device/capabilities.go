package device

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Capabilities is a read-only, non-sensitive snapshot of the Android runtime.
// It deliberately excludes device identifiers, credentials and user data.
type Capabilities struct {
	ReadOnly       bool            `json:"readOnly"`
	OS             string          `json:"os"`
	Kernel         string          `json:"kernel"`
	Arch           string          `json:"arch"`
	ABI            string          `json:"abi,omitempty"`
	Android        bool            `json:"android"`
	AndroidRelease string          `json:"androidRelease,omitempty"`
	AndroidSDK     string          `json:"androidSdk,omitempty"`
	Manufacturer   string          `json:"manufacturer,omitempty"`
	Model          string          `json:"model,omitempty"`
	UID            string          `json:"uid,omitempty"`
	Root           bool            `json:"root"`
	SELinux        string          `json:"selinux"`
	Commands       map[string]bool `json:"commands"`
	ReadablePaths  map[string]bool `json:"readablePaths"`
	WritablePaths  map[string]bool `json:"writablePaths"`
	Resolver       map[string]bool `json:"resolver"`
	SupportedABIs  []string        `json:"supportedAbis"`
	ExcludedData   []string        `json:"excludedData"`
}

// Collect gathers capabilities without creating, modifying or deleting files.
func Collect() Capabilities {
	kernel := output("/system/bin/uname", "-m")
	if kernel == "" {
		kernel = output("uname", "-m")
	}
	abi := property("ro.product.cpu.abi")
	android := abi != "" || exists("/system/bin") || strings.Contains(strings.ToLower(kernel), "android")

	commands := map[string]bool{
		"sh":       executable("/system/bin/sh", "sh"),
		"am":       executable("/system/bin/am", "am"),
		"getprop":  executable("/system/bin/getprop", "getprop"),
		"toybox":   executable("/system/bin/toybox", "toybox"),
		"curl":     executable("/system/bin/curl", "curl"),
		"unzip":    executable("/system/bin/unzip", "unzip"),
		"id":       executable("/system/bin/id", "id"),
		"ps":       executable("/system/bin/ps", "ps"),
		"getent":   executable("/system/bin/getent", "getent"),
		"nslookup": executable("/system/bin/nslookup", "nslookup"),
		"ping":     executable("/system/bin/ping", "ping"),
	}
	readablePaths := map[string]bool{
		"/proc":            readable("/proc"),
		"/sys":             readable("/sys"),
		"/system/bin":      readable("/system/bin"),
		"/data/local":      readable("/data/local"),
		"/etc/resolv.conf": readable("/etc/resolv.conf"),
	}
	writablePaths := map[string]bool{
		"/data/local":                writableByMode("/data/local"),
		"/data/local/ai-instruction": writableByMode("/data/local/ai-instruction"),
	}
	resolver := map[string]bool{
		"androidGetpropDNS": property("net.dns1") != "" || property("net.dns2") != "",
		"getent":            commands["getent"],
		"nslookup":          commands["nslookup"],
		"ping":              commands["ping"],
	}

	return Capabilities{
		ReadOnly:       true,
		OS:             runtime.GOOS,
		Kernel:         kernel,
		Arch:           runtime.GOARCH,
		ABI:            abi,
		Android:        android,
		AndroidRelease: property("ro.build.version.release"),
		AndroidSDK:     property("ro.build.version.sdk"),
		Manufacturer:   property("ro.product.manufacturer"),
		Model:          property("ro.product.model"),
		UID:            output("/system/bin/id", "-u"),
		Root:           os.Geteuid() == 0,
		SELinux:        selinuxState(),
		Commands:       commands,
		ReadablePaths:  readablePaths,
		WritablePaths:  writablePaths,
		Resolver:       resolver,
		SupportedABIs:  []string{"arm64-v8a", "armeabi-v7a", "x86_64", "x86"},
		ExcludedData:   []string{"IMEI", "serial number", "MAC address", "location", "contacts", "application data", "cookies", "API keys", "session contents"},
	}
}

// Prompt returns safe capability context for model instruction generation.
func (c Capabilities) Prompt() string {
	data, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return "本机 Android 设备能力（只读采集，不代表已执行任何操作）：\n" + string(data) +
		"\n请只生成与上述 ABI、Root 状态、SELinux、可用命令和可读写路径匹配的 ShortX 指令；不要假设未列出的命令存在，不要读取或输出任何设备标识、凭据、Cookie、应用数据或会话内容。"
}

func property(name string) string {
	for _, command := range []string{"/system/bin/getprop", "getprop"} {
		value := output(command, name)
		if value != "" {
			return value
		}
	}
	return ""
}

func output(command string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := exec.CommandContext(ctx, command, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func executable(candidates ...string) bool {
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return true
			}
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readable(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func writableByMode(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o222 != 0
}

func selinuxState() string {
	data, err := os.ReadFile("/sys/fs/selinux/enforce")
	if err != nil {
		return "unavailable"
	}
	switch strings.TrimSpace(string(data)) {
	case "1":
		return "enforcing"
	case "0":
		return "permissive"
	default:
		return "unknown"
	}
}
