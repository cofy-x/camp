// Camp's Go-installable entrypoint runs the matching complete release package.
package main

import "github.com/cofy-x/camp/internal/launcher"

var version = ""

func main() { launcher.Main(version) }
