# Uploader example files

`web/src/lib/engine/uploadFixtures.test.ts` parses each file with the real parser for its format and asserts a frozen stats snapshot. If a parser change moves the numbers below, update the test and this table together, and confirm the new numbers still make physical sense.

`Cube.gcode.gz` is a stub: only the slicer comment lines `parseGcodeStream` reads (OrcaSlicer 2.4.2 footer totals), gzipped like the server stores uploaded g-code (`CONFIG_DIR/uploads/<id>.gcode.gz`). The `.stl` and `.3mf` files are stored raw.

| File             | Format              | Parsed result                                   |
| ---------------- | ------------------- | ----------------------------------------------- |
| `Cube.gcode.gz`  | G-code (gzipped)    | 36.34 g, 12.18461 m, 3476 s (0h58m), cost 0.73  |
| `Cube.stl`       | STL (binary)        | 125 cm³, 12 triangles (50 mm cube)              |
| `Cube.3mf`       | 3MF (geometry only) | 19.683 cm³, 12 triangles (27 mm cube)           |

The STL and 3MF cubes are separate, unrelated models. All files are user-authored; no attribution is required.
