package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type tigrisTheme struct{}

func (t tigrisTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if variant == theme.VariantDark {
		return theme.DefaultTheme().Color(name, variant)
	}
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x0F, G: 0x5D, B: 0xD7, A: 0xFF}
	case theme.ColorNameFocus:
		return color.NRGBA{R: 0x3D, G: 0x7B, B: 0xE0, A: 0xFF}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0xD8, G: 0xE5, B: 0xFB, A: 0xFF}
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0xF5, G: 0xF7, B: 0xFA, A: 0xFF}
	case theme.ColorNameButton:
		return color.NRGBA{R: 0xE7, G: 0xEE, B: 0xF9, A: 0xFF}
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (t tigrisTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t tigrisTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t tigrisTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 10
	case theme.SizeNameInlineIcon:
		return 20
	default:
		return theme.DefaultTheme().Size(name)
	}
}
