// Package buildinfo contains the running application version, injected at build time.
package buildinfo

// Version is set by -ldflags "-X pansou/buildinfo.Version=<version>".
var Version = "dev"

const BootstrapVersion = 1
