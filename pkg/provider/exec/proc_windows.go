package exec

import osexec "os/exec"

// ponytail: Windows cancellation kills only the direct child, so a
// grandchild holding stdout keeps Run waiting; use a job object if
// that matters there.
func killGroupOnCancel(*osexec.Cmd) {}
