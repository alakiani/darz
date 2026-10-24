package link

import "testing"

func TestInstanceIfacesAreTheCoordinatorsNames(t *testing.T) {
	for name, want := range map[string]bool{
		"ways5i": true, "ways12l": true,
		// Names before the rename, which an upgraded Agent removes as strays.
		"dz5i": true, "dz12l": true,
		"ways0": false, "waysi": false, "ways5x": false, "ways5ai": false,
		"dzi": false, "eth0": false, "wg0": false,
	} {
		if got := isInstanceIface(name); got != want {
			t.Errorf("isInstanceIface(%q) = %v, want %v", name, got, want)
		}
	}
}
