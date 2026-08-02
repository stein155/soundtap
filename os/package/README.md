# SoundTap packages

Every compiled component of SoundTap Core is a Buildroot package here, with a pinned source,
hashes for what it downloads, license metadata and a deterministic install. Packages built from
this repository (`soundtapd`, the codec loader, the bootloaders) take the Git tree
as their integrity boundary and build without network access.
