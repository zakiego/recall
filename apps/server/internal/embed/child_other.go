package embed

import "os/exec"

// setChildAttrs: the child exits when its stdin closes, which happens when
// the parent exits for any reason (the fork's IPC "disconnect" in embed-child.ts).
// No Pdeathsig: Go may retire the thread that started the child, which would
// fire it while the parent is still alive.
func setChildAttrs(cmd *exec.Cmd) {}
