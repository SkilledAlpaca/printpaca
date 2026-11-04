# Third-party notices

printpaca's own code is AGPL-3.0-or-later licensed (see [LICENSE](LICENSE)). The shipped artifacts (the server binary and the embedded web bundle) statically link or bundle the third-party code listed below. All of it is permissively licensed (BSD-3-Clause, MIT, Apache-2.0, SIL OFL-1.1 for the bundled font, or public domain); none of it imposes copyleft or other obligations beyond notice reproduction, which is what this file is for.

This list covers only what actually ships at runtime: the Go module graph reachable from `server`'s `main` package (`go list -deps`, confirmed against the container's build target: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`), the npm packages whose code or data ends up in the built JS bundle (`web/dist/assets/*`), and the font file the web build serves. Build-only tooling (Vite, TypeScript, svelte-check, Playwright, etc.) never reaches a running container and is not listed here.

**Keep this file current**: update it whenever a runtime Go module or a bundled npm package changes (see CONTRIBUTING.md). Re-derive the Go list with `cd server && go list -deps -f '{{if .Module}}{{.Module.Path}}@{{.Module.Version}}{{end}}' . | sort -u`, and re-check `@mdi/js` and `svelte`'s own `LICENSE`/`package.json` files under `node_modules/`. Don't copy old text forward from memory.

## Go modules (statically linked into the server binary)

### modernc.org/sqlite v1.60.1: BSD-3-Clause (Go port) + bundles SQLite (public domain)

modernc.org/sqlite is a CGo-free, transpiled port of SQLite; it ships two notices: the Go port's own BSD-3-Clause license, and SQLite's own public domain dedication for the transpiled C source it's derived from.

> Copyright (c) 2017 The Sqlite Authors. All rights reserved.
>
> Redistribution and use in source and binary forms, with or without
> modification, are permitted provided that the following conditions are met:
>
> 1. Redistributions of source code must retain the above copyright notice, this
> list of conditions and the following disclaimer.
>
> 2. Redistributions in binary form must reproduce the above copyright notice,
> this list of conditions and the following disclaimer in the documentation
> and/or other materials provided with the distribution.
>
> 3. Neither the name of the copyright holder nor the names of its contributors
> may be used to endorse or promote products derived from this software without
> specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
> ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
> WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
> DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
> FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
> DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
> SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
> CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
> OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
> OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

SQLite itself (the transpiled deliverable code):

> SQLite Is Public Domain
>
> All of the code and documentation in SQLite has been dedicated to the public
> domain by the authors. All code authors, and representatives of the companies
> they work for, have signed affidavits dedicating their contributions to the
> public domain, and originals of those signed affidavits are stored in a
> firesafe at the main offices of Hwaci. Anyone is free to copy, modify,
> publish, use, compile, sell, or distribute the original SQLite code, either
> in source code form or as a compiled binary, for any purpose, commercial or
> non-commercial, and by any means.

modernc.org/sqlite also bundles the source for an optional `sqlite-vec` extension (MIT, Copyright (c) 2024 Alex Garcia); printpaca never loads it (no `vec0`/extension-loading calls anywhere in `server/`), but it is included here defensively since its source ships in the same module tree.

### modernc.org/libc v1.77.1, modernc.org/mathutil v1.7.1, modernc.org/memory v1.12.1: BSD-3-Clause

Transitive dependencies of modernc.org/sqlite (the runtime support library the transpiled C code needs, big-integer math, and a memory allocator). Same license text as modernc.org/sqlite above, with the copyright holder changed per module:

- modernc.org/libc: Copyright (c) 2017 The Libc Authors. All rights reserved.
- modernc.org/mathutil: Copyright (c) 2014 The mathutil Authors. All rights reserved.
- modernc.org/memory: Copyright (c) 2017 The Memory Authors. All rights reserved.

modernc.org/libc and modernc.org/memory also carry code from other projects (from their `LICENSE-3RD-PARTY.md`, `LICENSE-GO`, and `LICENSE-MMAP-GO` files), which is linked into the binary through modernc.org/sqlite:

- Go (Copyright (c) 2009 The Go Authors): BSD-3-Clause, same text as golang.org/x/sys below, with "Neither the name of Google Inc.".
- musl libc (Copyright © 2005-2020 Rich Felker, et al.): MIT. modernc.org/libc reproduces musl's C library code in Go. The MIT terms are the same as github.com/dustin/go-humanize below. The full list of musl authors and contributors is in modernc.org/libc's `LICENSE-3RD-PARTY.md`.
- go-netdb (Copyright (c) 2012 Dominik Honnef): MIT, same terms.
- NixOS/nixpkgs (Copyright (c) 2003-2025 Eelco Dolstra and the Nixpkgs/NixOS contributors): MIT, same terms.
- mmap-go, used by modernc.org/memory (Copyright (c) 2011, Evan Shaw
  <edsrzf@gmail.com>): BSD-3-Clause, same text as modernc.org/sqlite above
  with "Neither the name of the copyright holder".

### github.com/remyoudompheng/bigfft (pseudo-version 20230129092748-24d4a6f8daec): BSD-3-Clause

Used by modernc.org/mathutil for large-integer multiplication.

> Copyright (c) 2012 The Go Authors. All rights reserved.

Same BSD-3-Clause text as modernc.org/sqlite above, with "Neither the name of Google Inc." in the non-endorsement clause.

### golang.org/x/sys v0.49.0, golang.org/x/crypto v0.58.0: BSD-3-Clause

> Copyright 2009 The Go Authors.

Same BSD-3-Clause text as modernc.org/sqlite above, with "Neither the name of Google LLC" in the non-endorsement clause. golang.org/x/crypto is linked for `bcrypt` (and its `blowfish` dependency), which hashes passwords.

### github.com/google/uuid v1.6.0: BSD-3-Clause

> Copyright (c) 2009,2014 Google Inc. All rights reserved.

Same BSD-3-Clause text as modernc.org/sqlite above, with "Neither the name of Google Inc." in the non-endorsement clause.

### github.com/dustin/go-humanize v1.0.1: MIT

> Copyright (c) 2005-2008 Dustin Sallings <dustin@spy.net>
>
> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in
> all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
> SOFTWARE.

### gopkg.in/yaml.v3 v3.0.1: MIT (ported files) + Apache-2.0 (remainder)

> This project is covered by two different licenses: MIT and Apache.
>
> The following files were ported to Go from C files of libyaml, and thus
> are still covered by their original MIT license, with the additional
> copyright staring in 2011 when the project was ported over:
>
>     apic.go emitterc.go parserc.go readerc.go scannerc.go
>     writerc.go yamlh.go yamlprivateh.go
>
> Copyright (c) 2006-2010 Kirill Simonov
> Copyright (c) 2006-2011 Kirill Simonov
>
> [MIT terms, same as go-humanize above]
>
> ### Apache License ###
>
> All the remaining project files are covered by the Apache license:
>
> Copyright (c) 2011-2019 Canonical Ltd
>
> Licensed under the Apache License, Version 2.0 (the "License");
> you may not use this file except in compliance with the License.
> You may obtain a copy of the License at
>
>     http://www.apache.org/licenses/LICENSE-2.0
>
> Unless required by applicable law or agreed to in writing, software
> distributed under the License is distributed on an "AS IS" BASIS,
> WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
> See the License for the specific language governing permissions and
> limitations under the License.

### Go standard library: BSD-3-Clause

The compiled binary also statically links the Go runtime and standard library. Same license text and copyright (Copyright (c) 2009 The Go Authors) as golang.org/x/sys above; the full text is also published at
<https://go.dev/LICENSE>.

## npm packages (bundled into the embedded web assets)

### svelte v5.57.2: MIT

The compiled Svelte component output includes Svelte's runtime helper functions, even though `svelte` itself is a devDependency (it's the compiler; part of what it emits is this runtime).

> Copyright (c) 2016-2025 [Svelte Contributors](https://github.com/sveltejs/svelte/graphs/contributors)
>
> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### clsx v2.1.1: MIT

Dependency of `svelte`; its class-name helper is bundled into the web build with Svelte's runtime.

> MIT License
>
> Copyright (c) Luke Edwards <luke.edwards05@gmail.com> (lukeed.com)
>
> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### esm-env v1.2.2: MIT

Dependency of `svelte`; its environment-flag module is bundled into the web build with Svelte's runtime.

> Copyright 2022 Benjamin McCann
>
> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the “Software”), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED “AS IS”, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

### @mdi/js v7.4.47: Apache-2.0 (icon path data) / MIT (surrounding code)

`@mdi/js`'s `package.json` declares `"license": "Apache-2.0"`, but its bundled `LICENSE` file (Pictogrammers Free License) splits that: the icon path data itself (what `@mdi/js` exports, and what `web/src/components/Icon.svelte` bundles verbatim into the built JS) is Apache-2.0; the surrounding non-icon code is MIT.

> Pictogrammers Free License
> --------------------------
>
> This icon collection is released as free, open source, and GPL friendly by
> the [Pictogrammers](http://pictogrammers.com/) icon group. You may use it
> for commercial projects, open source projects, or anything really.
>
> # Icons: Apache 2.0 (https://www.apache.org/licenses/LICENSE-2.0)
> Some of the icons are redistributed under the Apache 2.0 license. All other
> icons are either redistributed under their respective licenses or are
> distributed under the Apache 2.0 license.
>
> # Fonts: Apache 2.0 (https://www.apache.org/licenses/LICENSE-2.0)
> All web and desktop fonts are distributed under the Apache 2.0 license. Web
> and desktop fonts contain some icons that are redistributed under the Apache
> 2.0 license. All other icons are either redistributed under their respective
> licenses or are distributed under the Apache 2.0 license.
>
> # Code: MIT (https://opensource.org/licenses/MIT)
> The MIT license applies to all non-font and non-icon files.

### uqr v0.1.3: MIT

Generates the QR code on a printed invoice's PayPal payment block (`web/src/components/QrCode.svelte`). Zero dependencies; it embeds a port of Nayuki's QR Code generator library, covered by the same MIT notice.

> MIT License
>
> Copyright (c) Project Nayuki
> Copyright (c) 2023 Anthony Fu <https://github.com/antfu>
>
> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## Fonts (bundled into the embedded web assets)

### Figtree (variable, Latin subset): SIL Open Font License 1.1

The admin app and the shop set their text in Figtree. The file `web/src/assets/fonts/figtree-latin-wght-normal.woff2` is the `latin` variable-weight subset (U+0000-00FF Latin and Latin-1, plus common punctuation) from the `@fontsource-variable/figtree` 5.3.0 package, copied unmodified into the repository. The web build hashes it into `web/dist/assets/` and the server serves it from the same origin, so nothing loads from a CDN. The upstream project is https://github.com/erikdkennedy/figtree. The OFL permits bundling and redistribution with software, provided this notice and license text travel with it, and forbids selling the font on its own.

To re-check: `npm pack @fontsource-variable/figtree@5.3.0`, compare the file under `package/files/` with the copy in the repository, and compare `package/LICENSE` with the text below.

> Copyright 2022 The Figtree Project Authors (https://github.com/erikdkennedy/figtree) Figtree-Italic[wght].ttf: Copyright 2022 The Figtree Project Authors (https://github.com/erikdkennedy/figtree)
>
> This Font Software is licensed under the SIL Open Font License, Version 1.1.
> This license is copied below, and is also available with a FAQ at:
> http://scripts.sil.org/OFL
>
>
> -----------------------------------------------------------
> SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007
> -----------------------------------------------------------
>
> PREAMBLE
> The goals of the Open Font License (OFL) are to stimulate worldwide
> development of collaborative font projects, to support the font creation
> efforts of academic and linguistic communities, and to provide a free and
> open framework in which fonts may be shared and improved in partnership
> with others.
>
> The OFL allows the licensed fonts to be used, studied, modified and
> redistributed freely as long as they are not sold by themselves. The
> fonts, including any derivative works, can be bundled, embedded,
> redistributed and/or sold with any software provided that any reserved
> names are not used by derivative works. The fonts and derivatives,
> however, cannot be released under any other type of license. The
> requirement for fonts to remain under this license does not apply
> to any document created using the fonts or their derivatives.
>
> DEFINITIONS
> "Font Software" refers to the set of files released by the Copyright
> Holder(s) under this license and clearly marked as such. This may
> include source files, build scripts and documentation.
>
> "Reserved Font Name" refers to any names specified as such after the
> copyright statement(s).
>
> "Original Version" refers to the collection of Font Software components as
> distributed by the Copyright Holder(s).
>
> "Modified Version" refers to any derivative made by adding to, deleting,
> or substituting -- in part or in whole -- any of the components of the
> Original Version, by changing formats or by porting the Font Software to a
> new environment.
>
> "Author" refers to any designer, engineer, programmer, technical
> writer or other person who contributed to the Font Software.
>
> PERMISSION & CONDITIONS
> Permission is hereby granted, free of charge, to any person obtaining
> a copy of the Font Software, to use, study, copy, merge, embed, modify,
> redistribute, and sell modified and unmodified copies of the Font
> Software, subject to the following conditions:
>
> 1) Neither the Font Software nor any of its individual components,
> in Original or Modified Versions, may be sold by itself.
>
> 2) Original or Modified Versions of the Font Software may be bundled,
> redistributed and/or sold with any software, provided that each copy
> contains the above copyright notice and this license. These can be
> included either as stand-alone text files, human-readable headers or
> in the appropriate machine-readable metadata fields within text or
> binary files as long as those fields can be easily viewed by the user.
>
> 3) No Modified Version of the Font Software may use the Reserved Font
> Name(s) unless explicit written permission is granted by the corresponding
> Copyright Holder. This restriction only applies to the primary font name as
> presented to the users.
>
> 4) The name(s) of the Copyright Holder(s) or the Author(s) of the Font
> Software shall not be used to promote, endorse or advertise any
> Modified Version, except to acknowledge the contribution(s) of the
> Copyright Holder(s) and the Author(s) or with their explicit written
> permission.
>
> 5) The Font Software, modified or unmodified, in part or in whole,
> must be distributed entirely under this license, and must not be
> distributed under any other license. The requirement for fonts to
> remain under this license does not apply to any document created
> using the Font Software.
>
> TERMINATION
> This license becomes null and void if any of the above conditions are
> not met.
>
> DISCLAIMER
> THE FONT SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
> EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO ANY WARRANTIES OF
> MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT
> OF COPYRIGHT, PATENT, TRADEMARK, OR OTHER RIGHT. IN NO EVENT SHALL THE
> COPYRIGHT HOLDER BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
> INCLUDING ANY GENERAL, SPECIAL, INDIRECT, INCIDENTAL, OR CONSEQUENTIAL
> DAMAGES, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
> FROM, OUT OF THE USE OR INABILITY TO USE THE FONT SOFTWARE OR FROM
> OTHER DEALINGS IN THE FONT SOFTWARE.
