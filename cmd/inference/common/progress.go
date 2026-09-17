/*
 * Copyright (C) 2017 Canonical Ltd
 * Copyright (C) 2026 Canonical Ltd
 *
 * Portions of this file are adapted from snapd's progress/ansimeter.go and
 * strutil/quantity/quantity.go:
 * https://github.com/canonical/snapd
 *
 * snapd attributes its quantity formatting code to
 * github.com/chipaca/quantity, used with permission.
 *
 * Modified in 2026 for the inference project.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program. If not, see <https://www.gnu.org/licenses/>.
 */

package common

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/canonical/inference/internal/snapd"
	"github.com/mattn/go-isatty"
	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

const (
	clearLine       = "\r\x1b[0m\x1b[K"
	hideCursor      = "\x1b[?25l"
	showCursor      = "\x1b[?25h"
	reverseVideo    = "\x1b[7m"
	normalVideo     = "\x1b[0m"
	defaultTermCols = 80
)

var spinnerFrames = [...]string{"/", "-", "\\", "|"}

type progressPrinter struct {
	w          io.Writer
	file       *os.File
	terminal   bool
	now        func() time.Time
	width      func() int
	taskID     string
	started    time.Time
	spin       int
	lineOpen   bool
	cursorHide bool
	lastLog    map[string]string
	reported   map[string]struct{}
}

func newProgressPrinter(w io.Writer) *progressPrinter {
	if w == nil {
		w = io.Discard
	}
	p := &progressPrinter{
		w:        w,
		now:      time.Now,
		width:    func() int { return defaultTermCols },
		lastLog:  make(map[string]string),
		reported: make(map[string]struct{}),
	}
	if file, ok := w.(*os.File); ok && isatty.IsTerminal(file.Fd()) {
		p.file = file
		p.terminal = true
		p.width = func() int {
			size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
			if err != nil || size.Col == 0 {
				return defaultTermCols
			}
			return int(size.Col)
		}
	}
	return p
}

func (p *progressPrinter) Update(change snapd.Change) {
	if !p.terminal {
		p.reportStartedTasks(change.Tasks)
	}
	for i := range change.Tasks {
		if change.Tasks[i].Status == "Wait" {
			p.notifyLastLog(change.Tasks[i])
		}
	}

	task := activeTask(change.Tasks)
	if task == nil {
		return
	}
	if task.Progress.Total <= 1 {
		p.spinTask(*task)
	} else {
		p.setTaskProgress(*task)
	}
	p.notifyLastLog(*task)
}

func (p *progressPrinter) reportStartedTasks(tasks []snapd.Task) {
	for _, task := range tasks {
		switch task.Status {
		case "Doing", "Done", "Wait", "Error", "Undoing", "Undone":
		default:
			continue
		}
		if task.Summary == "" {
			continue
		}
		key := task.ID + "\x00" + task.Summary
		if _, ok := p.reported[key]; ok {
			continue
		}
		p.Notify(task.Summary)
		p.reported[key] = struct{}{}
	}
}

func activeTask(tasks []snapd.Task) *snapd.Task {
	doing := 0
	for i := range tasks {
		if tasks[i].Status == "Doing" {
			doing++
		}
	}
	for i := len(tasks) - 1; i >= 0; i-- {
		task := &tasks[i]
		if task.Status != "Doing" && task.Status != "Wait" {
			continue
		}
		if doing > 1 && (task.Kind == "check-rerefresh" ||
			task.Kind == "process-delayed-security-backend-effects") {
			continue
		}
		return task
	}
	return nil
}

func (p *progressPrinter) spinTask(task snapd.Task) {
	if !p.terminal {
		return
	}
	p.startTask(task.ID)
	width := p.width()
	if width <= 0 {
		return
	}

	labelWidth := width
	suffix := ""
	if width >= 2 {
		labelWidth = width - 2
		suffix = " " + spinnerFrames[p.spin]
		p.spin = (p.spin + 1) % len(spinnerFrames)
	}
	label := strings.TrimRight(fitText(task.Summary, labelWidth), " ")
	fmt.Fprintf(p.w, "\r%s%s", label, suffix)
	p.lineOpen = true
}

func (p *progressPrinter) setTaskProgress(task snapd.Task) {
	if !p.terminal {
		return
	}
	p.startTask(task.ID)

	total := float64(task.Progress.Total)
	done := math.Max(0, math.Min(float64(task.Progress.Done), total))
	width := p.width()
	if width <= 0 {
		return
	}

	suffix := p.progressSuffix(done, total, width)
	labelWidth := width - runewidth.StringWidth(suffix)
	line := fitText(task.Summary, labelWidth) + suffix
	filled := int(done * float64(runewidth.StringWidth(line)) / total)
	before, after := splitAtWidth(line, filled)
	fmt.Fprintf(p.w, "\r%s%s%s%s", reverseVideo, before, normalVideo, after)
	p.lineOpen = true
}

func (p *progressPrinter) startTask(id string) {
	if p.taskID == id {
		return
	}
	p.taskID = id
	p.started = p.now()
	p.spin = 0
	if !p.cursorHide {
		fmt.Fprint(p.w, hideCursor)
		p.cursorHide = true
	}
}

func (p *progressPrinter) progressSuffix(done, total float64, width int) string {
	if width <= 15 {
		return ""
	}

	elapsed := p.now().Sub(p.started).Seconds()
	eta := " " + formatETA(done, total, elapsed)
	if width <= 20 {
		return eta
	}

	percent := fmt.Sprintf(" %3.0f%%", 100*done/total)
	if width <= 29 {
		return percent + eta
	}
	return percent + " " + formatBPS(done, elapsed) + eta
}

func (p *progressPrinter) notifyLastLog(task snapd.Task) {
	if len(task.Log) == 0 {
		return
	}
	message := task.Log[len(task.Log)-1]
	if message == "" || p.lastLog[task.ID] == message {
		return
	}
	p.Notify(message)
	p.lastLog[task.ID] = message
}

func (p *progressPrinter) Spin(message string) {
	if !p.terminal {
		key := "\x00" + message
		if _, ok := p.reported[key]; !ok {
			p.Notify(message)
			p.reported[key] = struct{}{}
		}
		return
	}
	p.spinTask(snapd.Task{ID: message, Summary: message, Status: "Doing"})
}

func (p *progressPrinter) Notify(message string) {
	if message == "" {
		return
	}
	if p.terminal {
		fmt.Fprint(p.w, clearLine)
		p.lineOpen = false
	}
	fmt.Fprintln(p.w, message)
}

func (p *progressPrinter) Finished() {
	if !p.terminal {
		return
	}
	if p.lineOpen {
		fmt.Fprint(p.w, clearLine)
	}
	if p.cursorHide {
		fmt.Fprint(p.w, showCursor)
	}
	p.lineOpen = false
	p.cursorHide = false
}

func fitText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= width {
		return text + strings.Repeat(" ", width-runewidth.StringWidth(text))
	}
	if width == 1 {
		return "."
	}
	prefix, _ := splitAtWidth(text, width-1)
	return prefix + "."
}

func splitAtWidth(text string, width int) (string, string) {
	if width <= 0 {
		return "", text
	}
	used := 0
	for index, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if used+runeWidth > width {
			return text[:index], text[index:]
		}
		used += runeWidth
		if used == width {
			next := index + len(string(r))
			return text[:next], text[next:]
		}
	}
	return text, ""
}

func formatBPS(done, elapsed float64) string {
	if done < 0 {
		done = -done
	}
	if elapsed < 0 {
		elapsed = -elapsed
	}
	if elapsed == 0 {
		return "    0B/s"
	}
	return formatAmount(uint64(done/elapsed), 5) + "B/s"
}

func formatETA(done, total, elapsed float64) string {
	if done <= 0 || elapsed <= 0 {
		return "ages!"
	}
	seconds := (total - done) * elapsed / done
	if math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds > 365*24*60*60 {
		return "ages!"
	}
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return formatSubminuteDuration(seconds)
	case seconds < 600:
		minutes, remaining := divmod(seconds, 60)
		return fmt.Sprintf("%.0fm%02.0fs", minutes, remaining)
	}

	minutes := seconds / 60
	switch {
	case minutes < 99.95:
		return fmt.Sprintf("%3.1fm", minutes)
	case minutes < 10*60:
		hours, remaining := divmod(minutes, 60)
		return fmt.Sprintf("%.0fh%02.0fm", hours, remaining)
	case minutes < 24*60:
		hours, remaining := divmod(minutes, 60)
		if remaining < 10 {
			return fmt.Sprintf("%.0fh%.0fm", hours, remaining)
		}
		return fmt.Sprintf("%3.1fh", minutes/60)
	}

	hours := minutes / 60
	switch {
	case hours < 10*24:
		days, remaining := divmod(hours, 24)
		return fmt.Sprintf("%.0fd%02.0fh", days, remaining)
	case hours < 99.95*24:
		days, remaining := divmod(hours, 24)
		if remaining < 10 {
			return fmt.Sprintf("%.0fd%.0fh", days, remaining)
		}
		return fmt.Sprintf("%4.1fd", hours/24)
	}

	days := hours / 24
	if days < 2*365.25 {
		return fmt.Sprintf("%4.0fd", days)
	}
	years := days / 365.25
	switch {
	case years < 9.995:
		return fmt.Sprintf("%4.2fy", years)
	case years < 99.95:
		return fmt.Sprintf("%4.1fy", years)
	case years < 999.5:
		return fmt.Sprintf("%4.0fy", years)
	case years > math.MaxUint64 || uint64(years) == 0:
		return "ages!"
	default:
		return formatAmount(uint64(years), 4) + "y"
	}
}

func formatSubminuteDuration(seconds float64) string {
	if seconds >= 9.995 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	if seconds >= 0.9995 {
		return fmt.Sprintf("%.2fs", seconds)
	}

	prefix := 'm'
	for _, candidate := range "m\u00b5n" {
		prefix = candidate
		seconds *= 1000
		if seconds >= 0.9995 {
			break
		}
	}
	if seconds > 9.5 {
		return fmt.Sprintf("%3.0f%cs", seconds, prefix)
	}
	return fmt.Sprintf("%.1f%cs", seconds, prefix)
}

func divmod(value, divisor float64) (float64, float64) {
	quotient, fraction := math.Modf(value / divisor)
	return quotient, fraction * divisor
}

func formatAmount(amount uint64, width int) string {
	if amount <= 5000 {
		return fmt.Sprintf("%*d", width, amount)
	}

	value := float64(amount)
	prefix := 'k'
	for _, candidate := range "kMGTPEZY" {
		prefix = candidate
		value /= 1000
		if value < 999.5 {
			break
		}
	}

	numberWidth := width - 1
	digits := 3
	if value < 99.5 {
		digits--
		if value < 9.5 {
			digits--
			if value < 0.95 {
				digits--
			}
		}
	}
	precision := max(0, numberWidth-digits-1)
	formatted := fmt.Sprintf("%*.*f%c", numberWidth, precision, value, prefix)
	if value < 0.95 {
		return formatted[1:]
	}
	return formatted
}
