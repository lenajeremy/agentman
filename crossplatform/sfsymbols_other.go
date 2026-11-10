//go:build !darwin

package main

// drawSymbol has no SF Symbols to draw off Apple's platforms: their licence
// keeps them there. The screens use Lucide instead.
func drawSymbol(name string, size float64, weight int, box, scale float64) []byte { return nil }
