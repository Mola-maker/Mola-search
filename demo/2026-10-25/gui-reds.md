# What the GUI suite fails on this Windows box, and why

Recorded 2026-10-25, on the fused tree at `15a263d`. The full suite,
`go test ./internal/gui -count=1`, ends with 11 failures in 221s. None of
them is caused by MolaSearch's own work, and this file exists so that
claim can be checked rather than taken on trust.

## How that was established

The same 11 tests were run on the tree as it was **before** G005's six
database panels and G006's pre-rendered report were restored — the
fusion commit `66715b6`, checked out in a throwaway worktree:

```
git worktree add .tmp-base 66715b6
cd .tmp-base/magpie
go test ./internal/gui -run 'TestLodyImportHandoff|TestImportStatusUnreadableIsUnknown|TestAionUiImportRow|TestPluginPage|TestProxyCheckSaysAProxyPassedBy|TestUpdateThatWontStart' -count=1
```

All 11 fail there identically (`/tmp/base-gui.log`). They fail the same
way with the restored code (`/tmp/gui-full.log`), so the restore neither
introduced nor fixed any of them.

## The failures

| Test | Why it fails here |
| --- | --- |
| `TestLodyImportHandoff` | `%AppData% is not defined`. The test's `sandboxHome` sets `APPDATA` and `LOCALAPPDATA` to `""`, so the Windows path lookup expands an empty variable and Go's `filepath.Join` leaves the literal `%AppData%` in the name. |
| `TestImportStatusUnreadableIsUnknown` | same `%AppData%` cause |
| `TestAionUiImportRow` | same `%AppData%` cause |
| `TestPluginPageServedShutIn`, `…ReadsWhatItDeclared`, `…NoAPI`, `…WebGuard`, `…Actions`, `…WindowPolicy` (6) | the plugin-page tests need a symlink, which this Windows account cannot create without developer mode or elevation |
| `TestUpdateThatWontStart` | `%1 is not a valid Win32 application`: the test writes a fake replacement binary and expects the update path to reject it by inspection; Windows refuses to exec it first |
| `TestProxyCheckSaysAProxyPassedBy` | `HTTP 502`, `the proxy asked 0 times`. The test sets `ALL_PROXY` and `NO_PROXY` and expects the request to be passed by; on this box the request reached the loopback rig and got a 502 from the proxy handler instead |

## What this means for a change here

These are not "flaky under load" and they are not a licence to ignore the
package. A GUI change still has to run the tests it can reach; the 11
above are a known-red baseline to compare against, not a passing grade.
Anything beyond these 11 is a real red.

The two causes worth fixing upstream rather than working around:

- `sandboxHome` should set `APPDATA` and `LOCALAPPDATA` to real
  directories under the temp home, not to `""`. That would fix 3 tests
  and is a genuine test bug, not a platform difference.
- The proxy test assumes the platform's proxy resolution honours
  `ALL_PROXY`/`NO_PROXY` the way the CI runners do.
