// Command winres writes rsrc_windows_amd64.syso: the icon, version information
// and manifest that Windows shows for go-web-side.exe. The Go linker picks the
// .syso file up automatically when building for Windows, so no extra tool is
// needed to build; this program only has to be re-run when the icon, manifest
// or version changes:
//
//	go run ./tools/winres
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// defaultVersion must match installer.Version (a test checks that).
const defaultVersion = "1.0.0"

// Resource types and the one language used throughout (US English).
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langEnglish = 0x0409
)

// iconSizes are the square sizes shipped as winres/icon-<size>.png.
var iconSizes = []int{16, 32, 48, 64, 256}

type resource struct {
	typ, id, lang uint16
	data          []byte
}

func main() {
	dir := flag.String("dir", "winres", "folder with icon-<size>.png files and manifest.xml")
	out := flag.String("out", "rsrc_windows_amd64.syso", "file to write")
	version := flag.String("version", defaultVersion, "product version, e.g. 1.0.0")
	flag.Parse()

	res, err := collect(*dir, *version)
	if err != nil {
		log.Fatal(err)
	}
	obj, err := buildObject(res)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, obj, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes, %d resources)\n", *out, len(obj), len(res))
}

// collect gathers every resource to embed.
func collect(dir, version string) ([]resource, error) {
	var res []resource

	// Icons: one RT_ICON per size (PNG-compressed), tied together by a RT_GROUP_ICON.
	var group bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&group, le, [3]uint16{0, 1, uint16(len(iconSizes))}) // reserved, type 1 = icon, count
	for i, size := range iconSizes {
		png, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("icon-%d.png", size)))
		if err != nil {
			return nil, err
		}
		id := uint16(i + 1)
		res = append(res, resource{rtIcon, id, langEnglish, png})
		dim := byte(size)
		if size >= 256 {
			dim = 0 // 0 means 256 in an icon directory
		}
		group.Write([]byte{dim, dim, 0, 0})                            // width, height, palette size, reserved
		binary.Write(&group, le, struct{ Planes, Bits uint16 }{1, 32}) // planes, bits per pixel
		binary.Write(&group, le, struct {
			Bytes uint32
			ID    uint16
		}{uint32(len(png)), id})
	}
	res = append(res, resource{rtGroupIcon, 1, langEnglish, group.Bytes()})

	vi, err := versionInfo(version)
	if err != nil {
		return nil, err
	}
	res = append(res, resource{rtVersion, 1, langEnglish, vi})

	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.xml"))
	if err != nil {
		return nil, err
	}
	res = append(res, resource{rtManifest, 1, langEnglish, manifest})
	return res, nil
}

// versionInfo builds a VS_VERSIONINFO block, the data behind the Details tab in
// a file's Properties dialog.
func versionInfo(version string) ([]byte, error) {
	var parts [4]uint16
	for i, p := range strings.Split(version, ".") {
		if i >= 3 {
			return nil, fmt.Errorf("version %q: want major.minor.patch", version)
		}
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("version %q: %w", version, err)
		}
		parts[i] = uint16(n)
	}
	file := version + ".0"

	le := binary.LittleEndian
	var fixed bytes.Buffer // VS_FIXEDFILEINFO
	for _, v := range []uint32{
		0xFEEF04BD, 0x00010000, // signature, structure version
		uint32(parts[0])<<16 | uint32(parts[1]), uint32(parts[2])<<16 | uint32(parts[3]), // file version
		uint32(parts[0])<<16 | uint32(parts[1]), uint32(parts[2])<<16 | uint32(parts[3]), // product version
		0x3F, 0, // flags mask, flags
		0x00040004, 1, 0, // VOS_NT_WINDOWS32, VFT_APP, subtype
		0, 0, // date
	} {
		binary.Write(&fixed, le, v)
	}

	strs := [][2]string{
		{"CompanyName", "S-Lab"},
		{"FileDescription", "S-Lab Setup"},
		{"FileVersion", file},
		{"InternalName", "go-web-side"},
		{"OriginalFilename", "go-web-side.exe"},
		{"ProductName", "S-Lab"},
		{"ProductVersion", version},
	}
	var table [][]byte
	for _, kv := range strs {
		v := utf16z(kv[1])
		table = append(table, node(kv[0], 1, uint16(len(v)/2), v))
	}
	stringTable := node("040904B0", 1, 0, nil, table...) // US English, Unicode
	stringFileInfo := node("StringFileInfo", 1, 0, nil, stringTable)

	var translation bytes.Buffer
	binary.Write(&translation, le, uint32(0x04B00409))
	varFileInfo := node("VarFileInfo", 1, 0, nil, node("Translation", 0, 4, translation.Bytes()))

	return node("VS_VERSION_INFO", 0, uint16(fixed.Len()), fixed.Bytes(), stringFileInfo, varFileInfo), nil
}

// node encodes one entry of a version-info tree: a header, its UTF-16 name, an
// optional value and its children, each part starting on a 4-byte boundary.
func node(key string, wType, valueLen uint16, value []byte, children ...[]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, valueLen, wType}) // length is filled in below
	b.Write(utf16z(key))
	pad(&b, 4)
	b.Write(value)
	for _, c := range children {
		pad(&b, 4)
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
}

func utf16z(s string) []byte {
	var b bytes.Buffer
	for _, u := range utf16.Encode([]rune(s + "\x00")) {
		binary.Write(&b, binary.LittleEndian, u)
	}
	return b.Bytes()
}

func pad(b *bytes.Buffer, align int) {
	for b.Len()%align != 0 {
		b.WriteByte(0)
	}
}

// buildObject lays the resources out as a COFF object file with a single .rsrc
// section, which is what the linker expects from a .syso file.
//
// Inside .rsrc the resources form a three-level directory (type, then id, then
// language) that ends in data entries; each entry holds the address of its data
// relative to the image, which the linker fixes up through a relocation.
func buildObject(res []resource) ([]byte, error) {
	sort.Slice(res, func(i, j int) bool {
		a, b := res[i], res[j]
		if a.typ != b.typ {
			return a.typ < b.typ
		}
		if a.id != b.id {
			return a.id < b.id
		}
		return a.lang < b.lang
	})
	for i := 1; i < len(res); i++ {
		if res[i].typ == res[i-1].typ && res[i].id == res[i-1].id && res[i].lang == res[i-1].lang {
			return nil, fmt.Errorf("duplicate resource type %d id %d", res[i].typ, res[i].id)
		}
	}

	// Group into types, and ids within a type (each id has exactly one language here).
	type idGroup struct {
		id   uint16
		idxs []int
	}
	type typeGroup struct {
		typ uint16
		ids []idGroup
	}
	var types []typeGroup
	for i, r := range res {
		if len(types) == 0 || types[len(types)-1].typ != r.typ {
			types = append(types, typeGroup{typ: r.typ})
		}
		t := &types[len(types)-1]
		if len(t.ids) == 0 || t.ids[len(t.ids)-1].id != r.id {
			t.ids = append(t.ids, idGroup{id: r.id})
		}
		g := &t.ids[len(t.ids)-1]
		g.idxs = append(g.idxs, i)
	}

	// Layout: root directory, type directories, id directories, data entries, data.
	const dirHeader, dirEntry, dataEntry = 16, 8, 16
	pos := dirHeader + dirEntry*len(types)
	typeOff := make([]int, len(types))
	idOff := make([][]int, len(types))
	for ti, t := range types {
		typeOff[ti] = pos
		pos += dirHeader + dirEntry*len(t.ids)
	}
	for ti, t := range types {
		idOff[ti] = make([]int, len(t.ids))
		for ii, g := range t.ids {
			idOff[ti][ii] = pos
			pos += dirHeader + dirEntry*len(g.idxs)
		}
	}
	entryOff := make([]int, len(res))
	for i := range res {
		entryOff[i] = pos
		pos += dataEntry
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		pos = (pos + 7) &^ 7
		dataOff[i] = pos
		pos += len(r.data)
	}
	raw := make([]byte, (pos+7)&^7)

	le := binary.LittleEndian
	writeDir := func(off, n int) { le.PutUint16(raw[off+14:], uint16(n)) } // number of ID entries
	writeEntry := func(off, idx int, id uint32, target int, sub bool) {
		at := off + dirHeader + dirEntry*idx
		le.PutUint32(raw[at:], id)
		if sub {
			target |= 0x80000000
		}
		le.PutUint32(raw[at+4:], uint32(target))
	}

	writeDir(0, len(types))
	for ti, t := range types {
		writeEntry(0, ti, uint32(t.typ), typeOff[ti], true)
		writeDir(typeOff[ti], len(t.ids))
		for ii, g := range t.ids {
			writeEntry(typeOff[ti], ii, uint32(g.id), idOff[ti][ii], true)
			writeDir(idOff[ti][ii], len(g.idxs))
			for li, ri := range g.idxs {
				writeEntry(idOff[ti][ii], li, uint32(res[ri].lang), entryOff[ri], false)
			}
		}
	}
	for i, r := range res {
		le.PutUint32(raw[entryOff[i]:], uint32(dataOff[i])) // relocated: offset from the section start
		le.PutUint32(raw[entryOff[i]+4:], uint32(len(r.data)))
		copy(raw[dataOff[i]:], r.data)
	}

	// COFF file: header, one section header, raw data, relocations, one symbol, string table.
	const fileHeader, sectionHeader, relocSize, symbolSize = 20, 40, 10, 18
	relocsAt := fileHeader + sectionHeader + len(raw)
	symbolsAt := relocsAt + relocSize*len(res)

	var b bytes.Buffer
	binary.Write(&b, le, struct {
		Machine, Sections    uint16
		Timestamp, SymbolsAt uint32
		NumSymbols           uint32
		OptionalSize, Flags  uint16
	}{0x8664, 1, 0, uint32(symbolsAt), 1, 0, 0}) // IMAGE_FILE_MACHINE_AMD64
	b.Write([]byte(".rsrc\x00\x00\x00"))
	binary.Write(&b, le, struct {
		VirtualSize, VirtualAddress, RawSize, RawAt, RelocsAt, LinesAt uint32
		NumRelocs, NumLines                                            uint16
		Characteristics                                                uint32
	}{0, 0, uint32(len(raw)), fileHeader + sectionHeader, uint32(relocsAt), 0,
		uint16(len(res)), 0, 0x40000040}) // initialized data, readable
	b.Write(raw)
	for i := range res {
		binary.Write(&b, le, struct {
			At, Symbol uint32
			Type       uint16
		}{uint32(entryOff[i]), 0, 3}) // IMAGE_REL_AMD64_ADDR32NB against the .rsrc symbol
	}
	b.Write([]byte(".rsrc\x00\x00\x00"))
	binary.Write(&b, le, struct {
		Value   uint32
		Section int16
		Type    uint16
		Class   uint8
		Aux     uint8
	}{0, 1, 0, 3, 0}) // static symbol naming section 1
	binary.Write(&b, le, uint32(4)) // empty string table: just its own length
	return b.Bytes(), nil
}
