# Rendered by scripts/generate-demo.sh; run that instead of vhs directly.
Output assets/demo.gif

Set Shell bash
Set FontSize 18
Set Width 1200
Set Height 640
Set Padding 28
Set Margin 20
Set MarginFill "#5B6B8C"
Set BorderRadius 10
Set WindowBar Colorful
Set Theme { "name": "drun", "black": "#1B2029", "red": "#FF6B6B", "green": "#5EE38E", "yellow": "#FFD866", "blue": "#61AFEF", "magenta": "#C678DD", "cyan": "#39D0D8", "white": "#E7EDF5", "brightBlack": "#7F8A9A", "brightRed": "#FF8787", "brightGreen": "#7CF0A6", "brightYellow": "#FFE08A", "brightBlue": "#82C0F5", "brightMagenta": "#D49BE8", "brightCyan": "#6FE3E9", "brightWhite": "#FFFFFF", "background": "#11151C", "foreground": "#E7EDF5", "selection": "#2A3140", "cursor": "#39D0D8" }
Set TypingSpeed 60ms

Hide
Type `export PATH="$DRUN_DEMO_BIN:$PATH" PS1='\[\e[38;2;127;138;154m\]$\[\e[0m\] '`
Enter
Type "clear"
Enter
Show

Sleep 600ms
Type "drun"
Sleep 400ms
Enter
Sleep 4s

Space
Sleep 600ms
Down
Sleep 400ms
Down
Sleep 400ms
Space
Sleep 1.2s
Enter
Sleep 6s

Type "clear"
Enter
Sleep 300ms
Type "drun grafana"
Sleep 400ms
Enter
Sleep 3s
Right
Sleep 800ms
Enter
Sleep 4.5s
