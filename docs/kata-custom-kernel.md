# Building a k8s-ready Kata guest kernel (host/infra)

The stock Kata guest kernels are deliberately minimal, so a real Kubernetes stack
hits **class-B gaps** — features simply *not compiled in*, which no vabbe trick can
conjure (see [node-profiles.md](node-profiles.md)). We've hit several already:

| Gap | Needed by |
| --- | --- |
| `xt_socket` (`CONFIG_NETFILTER_XT_MATCH_SOCKET`) | Cilium L7 proxy (TPROXY redirect) |
| `iscsi_tcp` (`CONFIG_ISCSI_TCP`) | Longhorn |
| `/proc/config.gz` (`CONFIG_IKCONFIG_PROC`) | kubeadm SystemVerification |
| `/proc/modules` (`CONFIG_MODULES`) | `lsmod`, Ansible `modprobe` |

Instead of whack-a-mole, **build one custom Kata kernel** with a generous k8s
config fragment that closes the whole category at once. This is **host/infra**
(belongs in the worker's Ansible provisioning, next to the Kata install) — **vabbe
itself doesn't change**. Once the kernel covers these natively you can even drop
some vabbe workarounds (`vabbe-kmod`'s `modules.builtin`, the kernel-config ship).

## Effort

Not "200 hours" — Kata ships a kernel builder. First build ≈ half a day (mostly
compile time + settling the fragment); after that it's a reproducible `vmlinux`
artifact you version. Maintenance = rebuild when Kata bumps the kernel.

## Build recipe

```sh
git clone https://github.com/kata-containers/kata-containers
cd kata-containers/tools/packaging/kernel

# 1. Fetch source + generate the .config from Kata's own fragments.
./build-kernel.sh -v 6.18.35 setup

# 2. Merge our k8s fragment into the generated kernel config (the file the script
#    just produced), e.g. append the CONFIG_* below and re-run olddefconfig, or
#    drop it as a fragment under configs/fragments/<arch>/ before `setup`.

# 3. Build + install (installs vmlinux into /opt/kata/share/kata-containers/).
./build-kernel.sh -v 6.18.35 build
sudo ./build-kernel.sh -v 6.18.35 install

# 4. Point Kata at it (host-wide):
#    /etc/kata-containers/configuration.toml
#      kernel = "/opt/kata/share/kata-containers/vmlinux-<built>.container"
sudo systemctl reload docker
```

(Exact flags vary by Kata version — `build-kernel.sh -h`.)

## The fragment — `kata-k8s.conf`

A **generous superset** for a k8s node with the common CNIs/storage. All `=y`
(built-in): the Kata guest has no `/lib/modules` to load `.ko` from, so built-in is
what works. **Enable only what you deploy** if you want to trim; cross-check each
project's docs per version (links below).

```ini
# ── class-B gaps we hit ─────────────────────────────────────────────
CONFIG_MODULES=y                          # /proc/modules → lsmod, ansible modprobe
CONFIG_IKCONFIG=y
CONFIG_IKCONFIG_PROC=y                     # /proc/config.gz → kubeadm config check

# ── kube-proxy / iptables / ipvs / conntrack ────────────────────────
CONFIG_BRIDGE=y
CONFIG_BRIDGE_NETFILTER=y
CONFIG_NF_CONNTRACK=y
CONFIG_NETFILTER_XT_MATCH_CONNTRACK=y
CONFIG_NETFILTER_XT_MATCH_COMMENT=y
CONFIG_NETFILTER_XT_MATCH_MULTIPORT=y
CONFIG_NETFILTER_XT_MATCH_ADDRTYPE=y
CONFIG_NETFILTER_XT_TARGET_REDIRECT=y
CONFIG_NETFILTER_XT_NAT=y
CONFIG_IP_NF_NAT=y
CONFIG_IP_VS=y
CONFIG_IP_VS_RR=y
CONFIG_IP_VS_WRR=y
CONFIG_IP_VS_SH=y
CONFIG_OVERLAY_FS=y

# ── tunneling (Cilium/Calico overlay) ───────────────────────────────
CONFIG_VXLAN=y
CONFIG_GENEVE=y
CONFIG_FIB_RULES=y
CONFIG_NET_IPIP=y                          # Calico IPIP mode
CONFIG_TUN=y
CONFIG_WIREGUARD=y                         # Cilium/Calico encryption

# ── Cilium: eBPF base ───────────────────────────────────────────────
CONFIG_BPF=y
CONFIG_BPF_SYSCALL=y
CONFIG_BPF_JIT=y
CONFIG_NET_CLS_BPF=y
CONFIG_NET_CLS_ACT=y
CONFIG_NET_SCH_INGRESS=y
CONFIG_CGROUP_BPF=y
CONFIG_PERF_EVENTS=y
CONFIG_SCHEDSTATS=y
CONFIG_CRYPTO_SHA1=y
CONFIG_CRYPTO_USER_API_HASH=y
CONFIG_XDP_SOCKETS=y                        # Calico eBPF / XDP
CONFIG_DEBUG_INFO_BTF=y                     # Cilium wants BTF (adds build time/size)

# ── Cilium: L7 proxy (TPROXY) — the current blocker ─────────────────
CONFIG_NETFILTER_XT_TARGET_TPROXY=y
CONFIG_NETFILTER_XT_TARGET_CT=y
CONFIG_NETFILTER_XT_MATCH_MARK=y
CONFIG_NETFILTER_XT_MATCH_SOCKET=y

# ── ipset / masquerade (non-BPF masq, Calico) ───────────────────────
CONFIG_IP_SET=y
CONFIG_IP_SET_HASH_IP=y
CONFIG_NETFILTER_XT_SET=y

# ── Longhorn ────────────────────────────────────────────────────────
CONFIG_ISCSI_TCP=y
CONFIG_SCSI_ISCSI_ATTRS=y
CONFIG_BLK_DEV_DM=y
CONFIG_DM_CRYPT=y                           # LUKS2 volume encryption
CONFIG_NFS_FS=y                             # backup (NFSv4) + RWX volumes
CONFIG_NFS_V4=y
CONFIG_NFS_V4_1=y
CONFIG_NFS_V4_2=y
CONFIG_NVME_TCP=y                           # Longhorn v2 (SPDK) data engine — optional

# ── Ceph / Rook (kernel rbd + cephfs clients) ───────────────────────
CONFIG_CEPH_LIB=y
CONFIG_BLK_DEV_RBD=y
CONFIG_CEPH_FS=y
CONFIG_LIBCRC32C=y
```

## Verify (after rebuild + reload)

On a Kata node:

```sh
zcat /proc/config.gz | grep -E 'XT_MATCH_SOCKET|ISCSI_TCP|CEPH_FS|WIREGUARD'  # all =y
modprobe xt_socket iscsi_tcp rbd          # no-op success (built-in)
```

Then re-run the installer: Cilium L7 should install its TPROXY rules, Longhorn's
`iscsid` should start, etc.

## Notes

- **All `=y`, not `=m`** — project docs often say `=m`, but the Kata guest can't
  load `.ko` (no `/lib/modules` tree), so compile in.
- This is a **superset**; trim to your CNI/storage. It's the trade-off of the Kata
  "own kernel" model: you own the feature set (see [node-profiles.md](node-profiles.md),
  class-B gaps).
- After this kernel, vabbe's `vabbe-kmod` (synthesized `modules.builtin`) and the
  kernel-config ship become belt-and-suspenders — harmless, and still useful on
  stock kernels.

## Sources

- [Cilium — System Requirements (kernel config)](https://docs.cilium.io/en/stable/operations/system_requirements/)
- [Calico — System requirements](https://docs.tigera.io/calico/latest/getting-started/bare-metal/requirements) · [eBPF dataplane](https://docs.tigera.io/calico/latest/operations/ebpf/enabling-ebpf)
- [Longhorn — Install prerequisites](https://longhorn.io/docs/1.12.0/deploy/install/) (`iscsi_tcp`, `dm_crypt`, NFSv4)
- [Rook/Ceph — kernel rbd/cephfs modules](https://rook.io/docs/rook/latest/Troubleshooting/ceph-common-issues/)
