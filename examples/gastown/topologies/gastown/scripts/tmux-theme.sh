#!/bin/sh
# tmux-theme.sh — Gas Town status bar theme
# Usage: tmux-theme.sh <session> <agent> <config-dir>
SESSION="$1" AGENT="$2" CONFIGDIR="$3"
tmux set-option -t "$SESSION" status-right-length 80
tmux set-option -t "$SESSION" status-interval 5
tmux set-option -t "$SESSION" status-right "#($CONFIGDIR/scripts/status-line.sh $AGENT) %H:%M"
