# Security

If you find a security problem in Brote, please tell me privately before you tell anyone else. GitHub has a way to do that: open the Security tab of this repository and choose Report a vulnerability. I'll answer as soon as I can, and I'd like to fix the problem before it's described in public.

What I most want to hear about is a way for a Scheme program to reach something the program that embeds it didn't register: a file, the network, a primitive that a gate refused, or the host's memory or time beyond what the host limited. A way to make the interpreter crash a host that runs it correctly counts too.

There's no release process yet beyond tags, so only the latest tagged version and `main` get fixes.
