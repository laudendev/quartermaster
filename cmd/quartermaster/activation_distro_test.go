package main

import "testing"

func TestProductFileForDistro(t *testing.T) {
	const dir = "/opt/quartermaster/products/popfocus/"
	cases := []struct{ product, platform, distro, want string }{
		{"POPF", "linux", "fedora", dir + "popfocus_x86_64.rpm"},
		{"POPF", "linux", "", dir + "popfocus_amd64.deb"},
		{"POPF", "linux", "debian", dir + "popfocus_amd64.deb"},
		{"POPF", "macos", "fedora", dir + "PopFocus.dmg"},
		{"POPF", "windows", "fedora", dir + "PopFocus-setup.exe"},
	}
	for _, c := range cases {
		got, err := productFileFor(c.product, c.platform, c.distro)
		if err != nil || got != c.want {
			t.Errorf("productFileFor(%q,%q,%q) = %q, %v; want %q", c.product, c.platform, c.distro, got, err, c.want)
		}
	}
}
