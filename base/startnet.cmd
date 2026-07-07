@echo off
rem pebuild startup script, run by winlogon via SYSTEM\Setup\CmdLine once the
rem environment reaches a command shell (cmd.exe /k startnet.cmd).
rem
rem Announce a completed boot on the first serial port, then leave an
rem interactive prompt open.

title pebuild
echo PEBUILD_BOOT_OK>COM1
