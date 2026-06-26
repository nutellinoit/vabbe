# syntax=docker/dockerfile:1
FROM rockylinux/rockylinux:9
COPY boot-id-token.service /etc/systemd/system/boot-id-token.service
RUN dnf -y install \
      systemd dbus \
      openssh-server openssh-clients sudo python3 python3-dnf \
      iproute iputils kmod iptables ipset ipvsadm procps-ng \
 && dnf clean all && rm -rf /var/cache/dnf \
 && mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh \
 && ssh-keygen -A \
 && systemctl enable sshd \
 && systemctl enable boot-id-token.service \
 && printf 'PermitRootLogin prohibit-password\nPasswordAuthentication no\nStrictModes no\n' \
      > /etc/ssh/sshd_config.d/vabbe.conf \
 # nsswitch lists `sss` first but sssd-client isn't installed; a lookup starting
 # at a missing nss module is host-dependent and can make PAM fail to resolve
 # root (sshd: "could not obtain user info (root)"). Resolve from files directly.
 && sed -i -E 's/^(passwd|group|shadow):.*/\1:     files systemd/' /etc/nsswitch.conf
RUN systemctl mask systemd-udevd.service systemd-udev-trigger.service \
      sys-kernel-debug.mount sys-kernel-tracing.mount \
      systemd-journald-audit.socket getty@tty1.service console-getty.service \
      2>/dev/null || true
STOPSIGNAL SIGRTMIN+3
VOLUME ["/var/lib/containerd"]
CMD ["/sbin/init"]
