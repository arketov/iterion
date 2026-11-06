// Package bots is the committed catalogue of bot bundles (one directory per
// bot: manifest, main.bot, prompts, skills) and the Go tests that hold every
// shipped workflow to the catalogue's contracts. It exports no Go API: the
// binary does not embed these bots (a project runs its own bots/), and a
// bundle is read from disk by pkg/botregistry.
package bots
