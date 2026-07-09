@echo off
rem pebuild startup hook: winlogon runs it at boot via Setup\CmdLine
rem (cmd.exe /k startnet.cmd), leaving an interactive prompt open.

title pebuild