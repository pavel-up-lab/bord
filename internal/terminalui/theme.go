package terminalui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ThemeName is the stable name stored in client state and accepted by --theme.
type ThemeName string

const (
	ThemeClassic ThemeName = "classic"
	ThemeAmber   ThemeName = "amber"
	ThemeGreen   ThemeName = "green"
	ThemeMono    ThemeName = "mono"
)

type Theme struct {
	Name                           ThemeName
	Background, Foreground         lipgloss.Color
	BarBackground, BarForeground   lipgloss.Color
	Accent, Muted                  lipgloss.Color
	SelectionBackground, Selection lipgloss.Color
	Lines                          LineSet
}

type LineSet struct{ Horizontal, Vertical, LeftTop, RightTop, LeftBottom, RightBottom string }

var doubleLines = LineSet{"═", "║", "╔", "╗", "╚", "╝"}

func DefaultLines() LineSet { return doubleLines }

// ThemeOption is one selectable built-in or imported Midnight Commander skin.
type ThemeOption struct {
	Name  ThemeName
	Label string
}

// ThemeCatalog owns the available palettes. Imported skins use stable mc:<file> names.
type ThemeCatalog struct {
	palettes map[ThemeName]Theme
	labels   map[ThemeName]string
	sources  map[ThemeName]string
	order    []ThemeName
}

func builtinCatalog() ThemeCatalog {
	c := ThemeCatalog{palettes: map[ThemeName]Theme{}, labels: map[ThemeName]string{}, sources: map[ThemeName]string{}}
	c.add(ThemeClassic, "Classic", Theme{ThemeClassic, lipgloss.Color("#0000A8"), lipgloss.Color("#E5E5E5"), lipgloss.Color("#00A8A8"), lipgloss.Color("#000000"), lipgloss.Color("#55FFFF"), lipgloss.Color("#A8A8A8"), lipgloss.Color("#00A8A8"), lipgloss.Color("#000000"), doubleLines})
	c.add(ThemeAmber, "Amber", Theme{ThemeAmber, lipgloss.Color("#1B1000"), lipgloss.Color("#FFD27A"), lipgloss.Color("#8A4F00"), lipgloss.Color("#FFF1C7"), lipgloss.Color("#FFB000"), lipgloss.Color("#B9873E"), lipgloss.Color("#FFB000"), lipgloss.Color("#1B1000"), doubleLines})
	c.add(ThemeGreen, "Green", Theme{ThemeGreen, lipgloss.Color("#001A08"), lipgloss.Color("#8CFF9B"), lipgloss.Color("#075D25"), lipgloss.Color("#D2FFD8"), lipgloss.Color("#39FF6A"), lipgloss.Color("#4FAE67"), lipgloss.Color("#39FF6A"), lipgloss.Color("#001A08"), doubleLines})
	c.add(ThemeMono, "Mono", Theme{ThemeMono, lipgloss.Color("#101010"), lipgloss.Color("#E8E8E8"), lipgloss.Color("#D8D8D8"), lipgloss.Color("#101010"), lipgloss.Color("#FFFFFF"), lipgloss.Color("#A0A0A0"), lipgloss.Color("#E8E8E8"), lipgloss.Color("#101010"), doubleLines})
	return c
}

func (c *ThemeCatalog) add(name ThemeName, label string, value Theme) {
	if _, exists := c.palettes[name]; !exists {
		c.order = append(c.order, name)
	}
	c.palettes[name], c.labels[name] = value, label
}

// DiscoverThemes imports valid MC skins from system and user skin directories.
// Invalid or unreadable third-party files are ignored without breaking startup.
func DiscoverThemes() ThemeCatalog {
	c := builtinCatalog()
	for _, path := range mcSkinFiles() {
		loaded, label, err := parseMCSkin(path)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		name := ThemeName("mc:" + strings.ToLower(base))
		loaded.Name = name
		c.add(name, label, loaded)
		c.sources[name] = string(name)
	}
	return c
}

func mcSkinFiles() []string {
	dirs := []string{"/usr/share/mc/skins", "/usr/local/share/mc/skins"}
	if values := os.Getenv("XDG_DATA_DIRS"); values != "" {
		for _, value := range filepath.SplitList(values) {
			dirs = append(dirs, filepath.Join(value, "mc", "skins"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "mc", "skins"), filepath.Join(home, ".config", "mc", "skins"))
	}
	if config, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(config, "mc", "skins"))
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		dirs = append(dirs, filepath.Join(value, "mc", "skins"))
	}
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		dirs = append(dirs, filepath.Join(value, "mc", "skins"))
	}
	seen := map[string]bool{}
	var paths []string
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.ini"))
		sort.Strings(matches)
		for _, path := range matches {
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// Resolve accepts a built-in name, an imported mc:<name>/filename, or a skin path.
func (c *ThemeCatalog) Resolve(value string) (ThemeName, error) {
	value = strings.TrimSpace(value)
	name := ThemeName(strings.ToLower(value))
	if _, ok := c.palettes[name]; ok {
		return name, nil
	}
	if _, ok := c.palettes[ThemeName("mc:"+strings.TrimSuffix(strings.ToLower(value), ".ini"))]; ok {
		return ThemeName("mc:" + strings.TrimSuffix(strings.ToLower(value), ".ini")), nil
	}
	if strings.ContainsAny(value, `/\`) || strings.HasSuffix(strings.ToLower(value), ".ini") {
		loaded, label, err := parseMCSkin(value)
		if err != nil {
			return "", err
		}
		base := strings.TrimSuffix(filepath.Base(value), filepath.Ext(value))
		name = ThemeName("mc:" + strings.ToLower(base))
		loaded.Name = name
		c.add(name, label, loaded)
		absolute, absoluteErr := filepath.Abs(value)
		if absoluteErr == nil {
			c.sources[name] = absolute
		} else {
			c.sources[name] = value
		}
		return name, nil
	}
	return "", fmt.Errorf("unknown theme %q", SanitizeTerminalText(value))
}

func (c ThemeCatalog) Setting(name ThemeName) string {
	if source := c.sources[name]; source != "" {
		return source
	}
	return string(name)
}

func (c ThemeCatalog) Options() []ThemeOption {
	options := make([]ThemeOption, 0, len(c.order))
	for _, name := range c.order {
		options = append(options, ThemeOption{Name: name, Label: c.labels[name]})
	}
	return options
}

func (c ThemeCatalog) Has(name ThemeName) bool {
	_, ok := c.palettes[name]
	return ok
}

func (c ThemeCatalog) Label(name ThemeName) string { return c.labels[name] }

func (c ThemeCatalog) Palette(name ThemeName) Theme {
	if value, ok := c.palettes[name]; ok {
		return value
	}
	return c.palettes[ThemeClassic]
}

func ParseTheme(value string) (ThemeName, error) {
	c := builtinCatalog()
	name, err := c.Resolve(value)
	if err != nil {
		return "", fmt.Errorf("%w (use classic, amber, green, or mono)", err)
	}
	return name, nil
}

func Palette(name ThemeName) Theme { return builtinCatalog().Palette(name) }

func parseMCSkin(path string) (Theme, string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Theme{}, "", fmt.Errorf("open MC skin: %w", err)
	}
	defer f.Close()
	sections, err := parseINI(io.LimitReader(f, 1<<20))
	if err != nil {
		return Theme{}, "", err
	}
	aliases := sections["aliases"]
	resolve := func(value string, fallback lipgloss.Color) lipgloss.Color {
		value = strings.TrimSpace(value)
		for range 8 {
			alias, ok := aliases[value]
			if !ok {
				alias, ok = aliases[strings.ToLower(value)]
			}
			if ok {
				value = strings.TrimSpace(alias)
				continue
			}
			break
		}
		if value == "" || strings.EqualFold(value, "default") {
			return fallback
		}
		if hex := normalizeMCColor(value); hex != "" {
			return lipgloss.Color(hex)
		}
		return fallback
	}
	base := builtinCatalog().Palette(ThemeClassic)
	pair := func(section, key string, fallbackFG, fallbackBG lipgloss.Color) (lipgloss.Color, lipgloss.Color) {
		parts := strings.Split(sections[section][key], ";")
		fg, bg := fallbackFG, fallbackBG
		if len(parts) > 0 {
			fg = resolve(parts[0], fg)
		}
		if len(parts) > 1 {
			bg = resolve(parts[1], bg)
		}
		return fg, bg
	}
	foreground, background := pair("core", "_default_", base.Foreground, base.Background)
	barForeground, barBackground := pair("menu", "_default_", base.BarForeground, base.BarBackground)
	selection, selectionBackground := pair("core", "selected", base.Selection, base.SelectionBackground)
	accent, _ := pair("core", "header", base.Accent, background)
	muted, _ := pair("core", "disabled", base.Muted, background)
	label := strings.TrimSpace(sections["skin"]["description"])
	if label == "" {
		label = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	label = SanitizeTerminalText(label)
	if label == "" {
		return Theme{}, "", errors.New("MC skin has no usable name")
	}
	lines := doubleLines
	lineValues := sections["lines"]
	for key, target := range map[string]*string{"dhoriz": &lines.Horizontal, "dvert": &lines.Vertical, "dlefttop": &lines.LeftTop, "drighttop": &lines.RightTop, "dleftbottom": &lines.LeftBottom, "drightbottom": &lines.RightBottom} {
		if value := SanitizeTerminalText(strings.TrimSpace(lineValues[key])); TextWidth(value) == 1 {
			*target = value
		}
	}
	return Theme{"", background, foreground, barBackground, barForeground, accent, muted, selectionBackground, selection, lines}, label, nil
}

func parseINI(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	section := ""
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if sections[section] == nil {
				sections[section] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			continue
		}
		sections[section][strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read MC skin: %w", err)
	}
	if sections["core"]["_default_"] == "" {
		return nil, errors.New("MC skin is missing core._default_")
	}
	return sections, nil
}

func normalizeMCColor(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "#") && (len(value) == 4 || len(value) == 7) {
		return value
	}
	named := map[string]string{
		"black": "#000000", "gray": "#808080", "lightgray": "#C0C0C0", "white": "#FFFFFF",
		"red": "#800000", "brightred": "#FF0000", "green": "#008000", "brightgreen": "#00FF00",
		"brown": "#808000", "yellow": "#FFFF00", "blue": "#000080", "brightblue": "#0000FF",
		"magenta": "#800080", "brightmagenta": "#FF00FF", "cyan": "#008080", "brightcyan": "#00FFFF",
	}
	if color := named[value]; color != "" {
		return color
	}
	if strings.HasPrefix(value, "color") {
		index, err := strconv.Atoi(strings.TrimPrefix(value, "color"))
		if err == nil && index >= 0 && index <= 255 {
			return xterm256(index)
		}
	}
	if strings.HasPrefix(value, "rgb") {
		digits := strings.TrimPrefix(value, "rgb")
		if len(digits) == 3 && digits[0] >= '0' && digits[0] <= '5' && digits[1] >= '0' && digits[1] <= '5' && digits[2] >= '0' && digits[2] <= '5' {
			levels := []int{0, 95, 135, 175, 215, 255}
			return fmt.Sprintf("#%02X%02X%02X", levels[digits[0]-'0'], levels[digits[1]-'0'], levels[digits[2]-'0'])
		}
	}
	if strings.HasPrefix(value, "gray") {
		index, err := strconv.Atoi(strings.TrimPrefix(value, "gray"))
		if err == nil && index >= 0 && index <= 23 {
			level := 8 + index*10
			return fmt.Sprintf("#%02X%02X%02X", level, level, level)
		}
	}
	return ""
}

func xterm256(index int) string {
	base := []string{
		"#000000", "#800000", "#008000", "#808000", "#000080", "#800080", "#008080", "#C0C0C0",
		"#808080", "#FF0000", "#00FF00", "#FFFF00", "#0000FF", "#FF00FF", "#00FFFF", "#FFFFFF",
	}
	if index < len(base) {
		return base[index]
	}
	if index < 232 {
		index -= 16
		levels := []int{0, 95, 135, 175, 215, 255}
		return fmt.Sprintf("#%02X%02X%02X", levels[index/36], levels[(index/6)%6], levels[index%6])
	}
	level := 8 + (index-232)*10
	return fmt.Sprintf("#%02X%02X%02X", level, level, level)
}
