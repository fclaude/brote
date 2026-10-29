# Security

If you find a security problem in Brote, please tell me privately before you tell anyone else. GitHub has a way to do that: open the Security tab of this repository and choose Report a vulnerability. I'll answer as soon as I can, and I'd like to fix the problem before it's described in public.

What I most want to hear about is a way for a Scheme program to reach something the program that embeds it didn't register or grant: a file outside the directories a `std` policy names, a host or address outside its network policy, a program it didn't allow, a module the policy leaves out, a primitive that a gate refused, or the host's memory or time beyond what the host limited. [Capabilities](docs/capabilities.md) says what the policy is meant to guarantee and what it isn't. A way to make the interpreter crash a host that runs it correctly counts too.

There's no release process yet beyond tags, so only the latest tagged version and `main` get fixes.
