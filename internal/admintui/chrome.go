package admintui

import "bord/internal/terminalui"

type action string

type command struct {
	label  string
	action action
}

func commandAt(commands []command, width, x int) (action, bool) {
	labels := make([]string, len(commands))
	for index := range commands {
		labels[index] = commands[index].label
	}
	index, ok := terminalui.CommandIndexAt(labels, width, x)
	if !ok {
		return "", false
	}
	return commands[index].action, true
}
