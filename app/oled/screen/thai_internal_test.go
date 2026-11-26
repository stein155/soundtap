// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// inkLevel is the coverage from which a pixel of a glyph counts as its ink: below it are
// the anti-aliased edges, which may brush a neighbour without the eye merging the two.
const inkLevel = 128

// thaiSamples are Thai texts beside the panel's own words: ที่ stacks a tone mark on an
// upper vowel the words do not stack it on.
//
//nolint:gochecknoglobals // a fixed list, only read
var thaiSamples = []string{"ที่", "ตั้งค่า", "เชื่อมต่อ", "ปุ่ม", "ปิ่น", "ฟั้น", "ป์"}

// thaiWords is every quoted text of the panel's words (app/internal/panel/words.go, as
// atlas.py reads it) that has a Thai letter, and thaiSamples.
func thaiWords(t *testing.T) []string {
	t.Helper()

	src, err := os.ReadFile("../../internal/panel/words.go")
	if err != nil {
		t.Fatal(err)
	}

	words := append([]string{}, thaiSamples...)
	for _, quoted := range regexp.MustCompile(`"[^"]*\p{Thai}[^"]*"`).FindAllString(string(src), -1) {
		words = append(words, quoted[1:len(quoted)-1])
	}

	return words
}

// thaiFaces are the atlas faces that have Thai letters.
func thaiFaces() []string {
	fontAtlas.once.Do(loadAtlas)

	var names []string

	for name, fc := range fontAtlas.faces {
		if _, ok := fc.glyphs['ก']; ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// markClashes counts the ink pixels a Thai mark of text shares with another glyph of it,
// drawn in fc from x 0.25 (a sub-pixel phase other than 0) on baseline 20.
func markClashes(fontFace face, text string) int {
	type inked struct {
		mark bool
		ink  map[[2]int]bool
	}

	var glyphs []inked

	fontFace.layout(text, 0.25, 20, func(char rune, drawn glyph, originX, originY int) {
		ink := map[[2]int]bool{}

		for row := range drawn.h {
			for col := range drawn.w {
				if fontAtlas.img.GrayAt(drawn.x+col, drawn.y+row).Y >= inkLevel {
					ink[[2]int{originX + col, originY + row}] = true
				}
			}
		}

		glyphs = append(glyphs, inked{mark: isThaiMark(char), ink: ink})
	})

	clashes := 0

	for index, mark := range glyphs {
		if !mark.mark {
			continue
		}

		for other := range glyphs {
			if other == index {
				continue
			}

			for px := range mark.ink {
				if glyphs[other].ink[px] {
					clashes++
				}
			}
		}
	}

	return clashes
}

// Every Thai mark the panel draws lands in pixels of its own: a tone mark over an upper
// vowel is raised above it, and a mark on ป ฝ ฟ clears the ascender, in every face. Drawn
// one letter at a time, without those forms, they collide.
func TestThaiMarksClearOfLetters(t *testing.T) {
	t.Parallel()

	words, faces := thaiWords(t), thaiFaces()
	if len(faces) == 0 {
		t.Fatal("no face has Thai letters")
	}

	plainClashes := 0

	for _, name := range faces {
		fc := faceOf(name)
		plain := fc
		plain.thai = nil

		for _, word := range words {
			if n := markClashes(fc, word); n > 0 {
				t.Errorf("%s %q: a Thai mark shares %d ink pixels with another letter", name, word, n)
			}

			plainClashes += markClashes(plain, word)
		}
	}

	if plainClashes == 0 {
		t.Error("the words drawn without the Thai forms do not collide either: the test sees nothing")
	}
}

// The Thai forms are marks: the text is as wide as before and has no glyph for a letter
// that is not Thai.
func TestThaiFormsKeepWidths(t *testing.T) {
	t.Parallel()

	for _, name := range thaiFaces() {
		fontFace := faceOf(name)
		for key, form := range fontFace.thai {
			if !isThaiMark(key[2]) || key[0] == 0 && key[1] == 0 {
				t.Errorf("%s: a form for %U after %U %U", name, key[2], key[0], key[1])
			}

			if len(form) != fontFace.phases {
				t.Errorf("%s: %d phases of %U, want %d", name, len(form), key[2], fontFace.phases)
			}
		}
	}
}
