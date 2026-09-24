package main

import "syscall"

func spawnAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
