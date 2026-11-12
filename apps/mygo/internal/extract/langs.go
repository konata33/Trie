package extract

// shikiLanguages are the keys of shiki 4.5.0's bundledLanguages (names and aliases): the reader format keeps a code
// block's requested language when it is one of them and calls it "text" otherwise.
var shikiLanguages = set(`abap actionscript-3 ada ahk ahk2 angular-html angular-ts apache apex apl applescript ara asciidoc asm astro awk
ballerina bat beancount berry bibtex bicep bird2 blade bsl c c3 cadence cairo chapel clarity clojure cmake cobol
codeowners codeql coffee common-lisp coq cpp crystal csharp css csv cue cypher d dart dax desktop diff docker dotenv
dream-maker edge elixir elm emacs-lisp erb erlang fennel fish fluent fortran-fixed-form fortran-free-form fsharp
gdresource gdscript gdshader genie gherkin git-commit git-rebase gleam glimmer-js glimmer-ts glsl gn gnuplot go graphql
groovy hack haml handlebars haskell haxe hcl hjson hlsl html html-derivative http hurl hxml hy imba ini java javascript
jinja jison json json5 jsonc jsonl jsonnet jssm jsx julia just kdl kotlin kusto latex lean less liquid llvm log logo lua
luau make markdown marko matlab mdc mdx mermaid mipsasm mojo moonbit move narrat nextflow nextflow-groovy nginx nim nix
nsis nushell objective-c objective-cpp ocaml odin openscad org pascal perl php pkl plsql po polar postcss powerquery
powershell prisma prolog proto pug puppet purescript python qml qmldir qss r racket raku razor rbs reg regexp rel riscv
ron rosmsg rst ruby rust sas sass scala scheme scss sdbl shaderlab shellscript shellsession smalltalk smithy solidity
soy sparql splunk sql ssh-config stata stylus surrealql svelte swift system-verilog systemd talonscript tasl tcl templ
terraform tex toml ts-tags tsv tsx turtle twig typescript typespec typst v vala vb verilog vhdl viml vue vue-html vue-
vine vyper wasm wenyan wgsl wikitext wit wolfram xml xsl yaml zenscript zig actionscript as3 ahk1 adoc batch cmd be bird
1c cdc chpl clj ql coffeescript lisp c++ c# cs cql dockerfile elisp erl ftl f for f77 f90 f95 f03 f08 f18 f# fs tscn
tres gd gjs gts gql hbs hs properties js cjs mjs fsl jl justfile kt kts kql lean4 makefile md mmd mips mbt mbti nar nf
nu objc scad pot potx ps ps1 pwsh protobuf jade py perl6 ruby-signature regex rb rs 1c-query shader bash sh shell zsh
console closure-templates spl styl surql talon tf tfvars lit ts cts mts tsp typ vim vimscript vy 文言 mediawiki wiki wl
yml`)
