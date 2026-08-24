@echo off
rem pebuild startup hook: winlogon runs it at boot via Setup\CmdLine
rem (cmd.exe /k startnet.cmd), leaving an interactive prompt open.

title pebuild

rem Walk the startup task list, if the startup plugin staged its runner. Guarded rather
rem than depended on: base must still boot as a bare prompt when that plugin is absent.
rem A desktop build never reaches this line: explorer-shell repoints Setup\CmdLine at
rem peshell.exe, which walks the same list itself.
if exist "%SystemRoot%\system32\perun.exe" "%SystemRoot%\system32\perun.exe" --stage pre-shell