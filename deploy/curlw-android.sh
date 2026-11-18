#!/system/bin/sh
# Runs the Android system curl in a clean environment. On the AGT phone the supervised process
# gets LD_LIBRARY_PATH pointing at the Linux layer, which makes /system/bin/curl fail to link.
# TLS verification is unchanged (no -k); no proxy variables are set on this host. Set TG_CURL to this file.
exec /system/bin/env -i /system/bin/curl "$@"
