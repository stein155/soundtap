// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

// Hello is the network card screen's words, a greeting and "almost done" in every language
// the panel speaks, in turn. They are fixed here rather than in the panel's word tables
// (app/internal/panel/words.go): the screen shows while the speaker's language is often not
// known yet, on the first install. golden.sh bakes their letters into the atlas.
//
//nolint:gochecknoglobals // a constant table
var Hello = [][2]string{
	{"Hallo!", "Bijna klaar"},       // nl
	{"Hello!", "Almost done"},       // en
	{"Bonjour !", "Presque fini"},   // fr
	{"¡Hola!", "Casi listo"},        // es
	{"Ciao!", "Quasi fatto"},        // it
	{"Hej!", "Nästan klart"},        // sv
	{"Hei!", "Melkein valmis"},      // fi
	{"Olá!", "Quase pronto"},        // pt
	{"Ahoj!", "Skoro hotovo"},       // cs
	{"Szia!", "Mindjárt kész"},      // hu
	{"Cześć!", "Prawie gotowe"},     // pl
	{"Salut!", "Aproape gata"},      // ro
	{"Привет!", "Почти готово"},     // ru
	{"Živjo!", "Skoraj končano"},    // sl
	{"Merhaba!", "Neredeyse bitti"}, // tr
	{"Γειά σου!", "Σχεδόν έτοιμο"},  // el
	{"こんにちは！", "もうすぐ完了"},            // ja
	{"안녕하세요!", "거의 다 됐어요"},          // ko
	{"สวัสดี!", "ใกล้เสร็จแล้ว"},    // th
	{"你好！", "快好了"},                  // zh-hans
	{"您好！", "快好了"},                  // zh-hant
}
