# Supported tools

Rigfile writes each AI tool's own configuration format to that tool's own location. Tools differ in what they can take, so support differs per category. The tables below are generated from the same capability files the CLI's adapters use, so they always match what `rigfile plan` does.

When a rig contains something a tool cannot take, the plan says so for that item and that tool. Nothing is dropped silently.

**base-secure** is enforced (as permission rules and hooks) only where the tool has a documented permission and hook contract. Everywhere else it is written as instructions the agent is asked to follow, which is weaker, and the plan says that too.

Where support is marked **UNVERIFIED**, the vendor's documentation did not settle it when checked, so Rigfile does not write it.
