package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// taskXML builds a Task Scheduler definition that:
//   - starts `jiggler run` at logon (InteractiveToken: it must run in the
//     user's desktop session to send input),
//   - re-triggers at each window start on scheduled days with WakeToRun, so a
//     sleeping PC wakes up and the loop is (re)started if it isn't running,
//   - never runs two copies, never times out, ignores battery state.
func taskXML(userID, exe, args string, s Schedule) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Headless scheduled mouse jiggler (` + appName + `). Wakes monitors during scheduled windows.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + esc(userID) + `</UserId>
    </LogonTrigger>
`)
	dayTags := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	var days strings.Builder
	for d, on := range s.Days {
		if on {
			days.WriteString("<" + dayTags[d] + "/>")
		}
	}
	for _, w := range s.Windows {
		start := time.Date(2024, 1, 1, w.Start/60, w.Start%60, 0, 0, time.Local)
		fmt.Fprintf(&b, `    <CalendarTrigger>
      <StartBoundary>%s</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByWeek>
        <DaysOfWeek>%s</DaysOfWeek>
        <WeeksInterval>1</WeeksInterval>
      </ScheduleByWeek>
    </CalendarTrigger>
`, start.Format("2006-01-02T15:04:05"), days.String())
	}
	b.WriteString(`  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + esc(userID) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>true</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + esc(exe) + `</Command>
      <Arguments>` + esc(args) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`)
	return b.String()
}
