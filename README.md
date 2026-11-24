# BlockDAG community stack source

Source for the BlockDAG community node, mining pool, payer, dashboards and
stack. The imported component source commits are recorded in
[source-subjects.json](source-subjects.json). Subsequent changes in this
repository are recorded in its Git history; original third-party copyright
and licensing notices are retained.

Project-original code is GPL-3.0-or-later. Upstream components retain their
respective licenses; see [notices](notices) and each component's license files.

The corresponding source release at [bdag-community/bdag-stack-releases](https://github.com/bdag-community/bdag-stack-releases/releases) includes the
offline Go module proxy used to build the node. Building the node requires Go
1.26.9 or newer and a C compiler for the target architecture.
