package releaseinfo

import "runtime"

const LinuxAMD64 = "linux-amd64"
const DarwinARM64 = "darwin-arm64"

type Platform struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RuntimePlatform string `json:"-"`
}

func Platforms() []Platform {
	return []Platform{{LinuxAMD64, "Ubuntu 24.04 amd64", "linux/x64"}, {DarwinARM64, "macOS 15+ Apple Silicon", "darwin/arm64"}}
}
func PlatformFor(id string) (Platform, bool) {
	for _, platform := range Platforms() {
		if platform.ID == id {
			return platform, true
		}
	}
	return Platform{}, false
}
func HostPlatform() (Platform, bool) { return PlatformFor(runtime.GOOS + "-" + runtime.GOARCH) }

func PlatformForRuntime(value string) (Platform, bool) {
	for _, platform := range Platforms() {
		if platform.RuntimePlatform == value {
			return platform, true
		}
	}
	return Platform{}, false
}
func InstallerFor(platform string) string { return "camp-runner-" + platform + ".tar.gz" }
func OfflineFor(platform string) string   { return "camp-runner-offline-" + platform + ".tar.gz" }
func RuntimeFor(platform, digest string) string {
	return "camp-runtime-" + platform + "-" + digest + ".tar.gz"
}
