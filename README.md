# mousejiggler-cli

Headless, scheduled "zen" mouse jiggler for Windows. Wakes your monitors and keeps them on during set hours. The cursor never moves, there's no window and no tray icon, and nothing changes about how you use the PC.

Default schedule: **Mon–Fri, 08:05–12:05 and 13:30–17:30**.

## How it works

- **Zen jiggle:** sends a `SendInput` relative mouse move of `0,0`. Windows counts it as user input, which resets the idle timer and wakes sleeping displays, but the pointer stays where it is. This is the same approach as the "zen jiggle" mode in arkane-systems/mousejiggler.
- **Keep-awake:** inside a window it also holds `SetThreadExecutionState(ES_DISPLAY_REQUIRED | ES_SYSTEM_REQUIRED)`. Outside a window it drops that request, so normal sleep and display-off settings apply again.
- **Scheduling:** `install` registers one Task Scheduler task. It starts the loop at logon and fires again at each window start on scheduled days, with **Wake to run** turned on. If the PC is asleep at 08:05 it wakes up. If the loop is already running, the extra trigger does nothing.
- **Headless:** the exe is built as a GUI-subsystem binary, so the scheduled task never flashes a console window. When you run it from a terminal, it attaches to that terminal so you can see its output.

## Install

1. Download `jiggler-windows-amd64.exe` (or `arm64`) from the repo's **Actions → build → Artifacts**, or from Releases if a `v*` tag exists. You can also build it yourself:
   ```
   GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui -s -w" -o jiggler.exe .
   ```
2. Rename the file to `jiggler.exe` and run it from PowerShell or cmd. You don't need admin:
   ```
   .\jiggler.exe install
   ```
   This copies the exe to `%LOCALAPPDATA%\Programs\mousejiggler-cli\jiggler.exe`, registers the `MouseJigglerCLI` task and starts it.

You can use a custom schedule. Re-running `install` overwrites the existing task:
```
.\jiggler.exe install --days Mon-Fri --window 08:05-12:05 --window 13:30-17:30 --interval 1m
```
Times use the **24h** clock. `--days` accepts `Mon-Fri`, `Mon,Wed,Fri`, `Sat-Sun` or `all`.

## Commands

| Command | What it does |
|---|---|
| `install [flags]` | Copies the exe, registers and starts the scheduled task |
| `uninstall` | Stops and deletes the task |
| `status [flags]` | Shows the schedule, whether it's active now, the next window and the task state |
| `once` | Sends one zen jiggle immediately (quick test) |
| `run [flags]` | Runs the loop in the foreground (this is what the task runs) |

Log: `%LOCALAPPDATA%\mousejiggler-cli\jiggler.log`. It records window enter/exit and errors only, and it truncates itself after 1 MB.

## Caveats

- **Waking from sleep** needs wake timers enabled: Control Panel → Power Options → Change plan settings → Advanced → Sleep → *Allow wake timers* → **Enable**. Some corporate images turn this off.
- **Locked workstation:** Windows won't let a user process inject input into the lock screen, so `SendInput` fails while the PC is locked (this shows up in the log). The keep-awake request still applies. In practice: if the session is unlocked when a window starts, the displays stay on for the whole window. If Windows locked overnight, the monitors may not wake until you touch the keyboard. Group Policy can force a lock-screen timeout, and no user-level tool can override that.
- The task runs only while you're logged in (it needs your desktop session). A reboot plus auto-logon, or simply logging in, starts it again.
- Changes to `--window`, `--days` or `--interval` only apply after you re-run `install`.
