// Package esp implements the ESP32-S3-BOX voice channel for obi-wan-core.
//
// The firmware posts raw int16 PCM audio to POST /talk. The server runs
// Whisper STT, submits the transcribed text as a core.Turn on channel
// "esp", then synthesises the reply via Piper and returns a WAV body.
//
// The firmware plays the WAV through the BOX speaker. The channel is
// first-class: its own memory, its own system prompt, its own entry in
// the unified history — identical to the R1 channel, except speech is
// captured on-device.
package esp
