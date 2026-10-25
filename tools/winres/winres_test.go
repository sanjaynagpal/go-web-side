package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"go-web-side/installer"
)

const winresDir = "../../winres"

func TestDefaultVersionMatchesInstaller(t *testing.T) {
	if defaultVersion != installer.Version {
		t.Fatalf("tools/winres defaultVersion = %q but installer.Version = %q; update one of them and re-run `go run ./tools/winres`",
			defaultVersion, installer.Version)
	}
}

// buildSyso builds the object from the real icons and manifest.
func buildSyso(t *testing.T) ([]resource, []byte) {
	t.Helper()
	res, err := collect(winresDir, defaultVersion)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := buildObject(res)
	if err != nil {
		t.Fatal(err)
	}
	return res, obj
}

// The standard library's PE reader must accept the object as a COFF file with
// one .rsrc section holding one relocation per resource.
func TestObjectIsValidCOFF(t *testing.T) {
	res, obj := buildSyso(t)
	path := filepath.Join(t.TempDir(), "rsrc.syso")
	if err := os.WriteFile(path, obj, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := pe.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if f.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Errorf("machine = %#x", f.FileHeader.Machine)
	}
	if len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" {
		t.Fatalf("sections = %+v", f.Sections)
	}
	if got := int(f.Sections[0].NumberOfRelocations); got != len(res) {
		t.Errorf("relocations = %d, want one per resource (%d)", got, len(res))
	}
	if len(f.COFFSymbols) != 1 || f.FileHeader.NumberOfSymbols != 1 {
		t.Errorf("symbols = %d", len(f.COFFSymbols))
	}
}

// Walk the resource directory the way Windows does and check what it finds.
func TestResourceTreeFindsEverything(t *testing.T) {
	res, obj := buildSyso(t)
	f, err := pe.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.Sections[0].Data()
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	count := func(dirAt int) int { return int(le.Uint16(raw[dirAt+12:])) + int(le.Uint16(raw[dirAt+14:])) }
	entry := func(dirAt, i int) (id uint32, off int, sub bool) {
		at := dirAt + 16 + 8*i
		off = int(le.Uint32(raw[at+4:]))
		return le.Uint32(raw[at:]), off &^ 0x80000000, off&0x80000000 != 0
	}

	found := map[uint32]int{} // resource type -> how many resources
	for ti := 0; ti < count(0); ti++ {
		typ, tOff, sub := entry(0, ti)
		if !sub {
			t.Fatalf("type %d is not a directory", typ)
		}
		for ii := 0; ii < count(tOff); ii++ {
			_, iOff, _ := entry(tOff, ii)
			for li := 0; li < count(iOff); li++ {
				_, dOff, isDir := entry(iOff, li)
				if isDir {
					t.Fatal("language level should point at data")
				}
				dataAt, size := int(le.Uint32(raw[dOff:])), int(le.Uint32(raw[dOff+4:]))
				if dataAt+size > len(raw) {
					t.Fatalf("type %d data runs past the section", typ)
				}
				found[typ]++
			}
		}
	}
	want := map[uint32]int{rtIcon: len(iconSizes), rtGroupIcon: 1, rtVersion: 1, rtManifest: 1}
	for typ, n := range want {
		if found[typ] != n {
			t.Errorf("type %d: found %d, want %d", typ, found[typ], n)
		}
	}
	if total := found[rtIcon] + found[rtGroupIcon] + found[rtVersion] + found[rtManifest]; total != len(res) {
		t.Errorf("tree has %d resources, built %d", total, len(res))
	}
}

func TestIconsAreSquarePNGs(t *testing.T) {
	res, _ := buildSyso(t)
	n := 0
	for _, r := range res {
		if r.typ != rtIcon {
			continue
		}
		if !bytes.HasPrefix(r.data, []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("icon %d is not a PNG", r.id)
		}
		w, h := binary.BigEndian.Uint32(r.data[16:]), binary.BigEndian.Uint32(r.data[20:])
		if w != h || int(w) != iconSizes[n] {
			t.Errorf("icon %d is %dx%d, want %dx%d", r.id, w, h, iconSizes[n], iconSizes[n])
		}
		n++
	}
	if n != len(iconSizes) {
		t.Errorf("found %d icons", n)
	}
}

type vnode struct {
	key   string
	typ   uint16
	value []byte
	kids  []vnode
}

// parseNode decodes one version-info entry exactly as Windows reads it.
func parseNode(t *testing.T, b []byte) vnode {
	t.Helper()
	le := binary.LittleEndian
	length, valueLen, typ := int(le.Uint16(b)), int(le.Uint16(b[2:])), le.Uint16(b[4:])
	if length > len(b) || length < 6 {
		t.Fatalf("bad node length %d (have %d bytes)", length, len(b))
	}
	b = b[:length]
	at := 6
	var key []uint16
	for ; at+1 < len(b); at += 2 {
		u := le.Uint16(b[at:])
		if u == 0 {
			at += 2
			break
		}
		key = append(key, u)
	}
	at = (at + 3) &^ 3
	n := vnode{key: string(utf16.Decode(key)), typ: typ}

	valueBytes := valueLen // binary values count bytes, text values count UTF-16 characters
	if typ == 1 {
		valueBytes = valueLen * 2
	}
	if at+valueBytes > len(b) {
		t.Fatalf("node %q: value runs past its end", n.key)
	}
	n.value = b[at : at+valueBytes]
	at += valueBytes
	for {
		at = (at + 3) &^ 3
		if at >= len(b) {
			break
		}
		kid := parseNode(t, b[at:])
		n.kids = append(n.kids, kid)
		at += int(le.Uint16(b[at:]))
	}
	return n
}

func TestVersionInfoParses(t *testing.T) {
	blob, err := versionInfo("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	root := parseNode(t, blob)
	if root.key != "VS_VERSION_INFO" || len(root.value) != 52 {
		t.Fatalf("root = %q with %d value bytes", root.key, len(root.value))
	}
	le := binary.LittleEndian
	if sig := le.Uint32(root.value); sig != 0xFEEF04BD {
		t.Errorf("fixed file info signature = %#x", sig)
	}
	if ms, ls := le.Uint32(root.value[8:]), le.Uint32(root.value[12:]); ms != 1<<16|2 || ls != 3<<16 {
		t.Errorf("file version = %#x %#x, want 1.2.3.0", ms, ls)
	}

	strs := map[string]string{}
	for _, info := range root.kids {
		if info.key != "StringFileInfo" {
			continue
		}
		for _, table := range info.kids {
			if table.key != "040904B0" {
				t.Errorf("string table %q", table.key)
			}
			for _, s := range table.kids {
				u := make([]uint16, len(s.value)/2)
				for i := range u {
					u[i] = le.Uint16(s.value[2*i:])
				}
				strs[s.key] = strings.TrimRight(string(utf16.Decode(u)), "\x00")
			}
		}
	}
	for key, want := range map[string]string{
		"FileDescription": "S-Lab Setup", "ProductName": "S-Lab", "CompanyName": "S-Lab",
		"ProductVersion": "1.2.3", "FileVersion": "1.2.3.0", "OriginalFilename": "go-web-side.exe",
	} {
		if strs[key] != want {
			t.Errorf("%s = %q, want %q", key, strs[key], want)
		}
	}
}

func TestVersionRejectsBadInput(t *testing.T) {
	for _, v := range []string{"", "1.x.0", "1.2.3.4", "99999.0.0"} {
		if _, err := versionInfo(v); err == nil {
			t.Errorf("versionInfo(%q) should fail", v)
		}
	}
}

func TestManifestAsksForNoElevation(t *testing.T) {
	m, err := os.ReadFile(filepath.Join(winresDir, "manifest.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(m, []byte(`level="asInvoker"`)) {
		t.Error("manifest must run as the invoking user; all-users installs elevate through their own UAC prompt")
	}
}
