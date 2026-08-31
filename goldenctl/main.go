// Command goldenctl consolidates the golden-image CI helper scripts into one
// tool: catalog editing, image-request intake, gate checks, policy reconcile,
// and the dashboard. Subcommands are added under cmd/.
package main

import "github.com/cartyc/golden-image/goldenctl/cmd"

func main() { cmd.Execute() }
