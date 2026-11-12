//go:build !darwin

package explore

import "github.com/egoist/mygo"

// seen and unseen are shots_darwin.go's: elsewhere a capture's page is left as its window's system draws it.
func seen(*mygo.Window)   {}
func unseen(*mygo.Window) {}
