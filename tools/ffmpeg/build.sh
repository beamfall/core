#!/bin/sh
# Build the ffmpeg the Beamfall Core image ships (CORE-2497).
#
# The FFmpeg tree and patch series are jellyfin-ffmpeg's locked source release
# (its tone-map filters and hardware fixes are what core drives), built against
# the libraries of the Debian snapshot named in the lock. The build is
# GPL-3.0-or-later and never links the Fraunhofer FDK AAC library or any other
# nonfree component: core encodes AAC with ffmpeg's native `aac` encoder.
#
# Runs as root in a debian:bookworm builder whose architecture is the build
# platform; a different TARGET_ARCH cross-compiles. Writes to OUT:
#   root/usr/lib/jellyfin-ffmpeg/   ffmpeg, ffprobe and lib/ (rpath to lib/)
#   runtime-packages.txt            exact Debian packages the runtime installs
#   source/<bundle>.tar, SHA256SUMS corresponding source for all of the above
#
# Usage: build.sh LOCK PIN TARGET_ARCH OUT
set -eux

lock="$1"
pin="$2"
target_arch="$3"
out="$4"
here="$(cd "$(dirname "$0")" && pwd)"

lockval() {
	v="$(sed -n "s/^$1=//p" "$lock")"
	test -n "$v" || { echo "build.sh: $1 missing from $lock" >&2; exit 1; }
	printf '%s\n' "$v"
}

printf '%s  %s\n' "$(lockval FFMPEG_PIN_SHA256)" "$pin" | sha256sum -c -
version="$(cat "$pin")"

build_arch="$(dpkg --print-architecture)"
case "$target_arch" in
	amd64) triplet=x86_64-linux-gnu ;;
	arm64) triplet=aarch64-linux-gnu ;;
	*) echo "build.sh: unsupported TARGET_ARCH $target_arch" >&2; exit 1 ;;
esac
cross=""
if [ "$target_arch" != "$build_arch" ]; then
	cross="${triplet}-"
fi

sh "$here/apt-snapshot.sh" "$lock"
if [ -n "$cross" ]; then
	dpkg --add-architecture "$target_arch"
fi
apt-get update

# Libraries ffmpeg links, as -dev packages for the target architecture.
dev="libass-dev libbluray-dev libdav1d-dev libdrm-dev libfontconfig-dev
	libfreetype-dev libfribidi-dev libgmp-dev libgnutls28-dev libharfbuzz-dev
	libmp3lame-dev libopenmpt-dev libopus-dev libsvtav1enc-dev libtheora-dev
	libvorbis-dev libvpx-dev libwebp-dev libx264-dev libx265-dev libxml2-dev
	libzimg-dev libzvbi-dev ocl-icd-opencl-dev"
# Intel VA-API and QSV (oneVPL) are amd64 only, as in jellyfin-ffmpeg.
# The drivers they load at run time are installed here too, so their exact
# versions and sources are recorded with the linked libraries.
hw_runtime=""
if [ "$target_arch" = amd64 ]; then
	dev="$dev libva-dev libvpl-dev"
	hw_runtime="intel-media-va-driver-non-free libmfx1 libmfx-gen1.2 intel-opencl-icd"
fi
# build-essential is the host compiler configure needs even when cross
# compiling; nasm assembles the x86 SIMD code.
tools="ca-certificates curl clang make pkgconf patch binutils dpkg-dev build-essential"
if [ "$target_arch" = amd64 ]; then
	tools="$tools nasm"
fi
if [ -n "$cross" ]; then
	tools="$tools crossbuild-essential-$target_arch binutils-$triplet"
	dev="$(for p in $dev; do printf '%s:%s ' "$p" "$target_arch"; done)"
fi
# shellcheck disable=SC2086
apt-get install -y --no-install-recommends $tools $dev $hw_runtime

work="$(mktemp -d)"
src_out="$out/source"
mkdir -p "$src_out/upstream" "$src_out/debian" "$src_out/build"

fetch() { # URL SHA256 -> $src_out/upstream/<name>
	name="${1##*/}"
	curl -fsSL --retry 5 "$1" -o "$src_out/upstream/$3$name"
	printf '%s  %s\n' "$2" "$src_out/upstream/$3$name" | sha256sum -c -
}
fetch "$(lockval FFMPEG_SOURCE_URL)" "$(lockval FFMPEG_SOURCE_SHA256)" jellyfin-ffmpeg-
fetch "$(lockval FFMPEG_NVCODEC_URL)" "$(lockval FFMPEG_NVCODEC_SHA256)" nv-codec-headers-

# NVIDIA codec headers (MIT, header only; the driver is the host's).
mkdir "$work/nv"
tar -xzf "$src_out/upstream/nv-codec-headers-"*.tar.gz -C "$work/nv" --strip-components=1
make -C "$work/nv" PREFIX=/usr/local install

if [ "$target_arch" = amd64 ]; then
	# AMD AMF headers (MIT, header only; the runtime is AMD's driver).
	fetch "$(lockval FFMPEG_AMF_URL)" "$(lockval FFMPEG_AMF_SHA256)" ""
	mkdir -p "$work/amf" /usr/local/include/AMF
	tar -xzf "$src_out/upstream/AMF-headers-"*.tar.gz -C "$work/amf" --strip-components=1
	cp -R "$work/amf/AMF/." /usr/local/include/AMF/
fi

mkdir "$work/ffmpeg"
tar -xzf "$src_out/upstream/jellyfin-ffmpeg-"*.tar.gz -C "$work/ffmpeg" --strip-components=1
cd "$work/ffmpeg"
test "$(cat VERSION)-Jellyfin" = "$version"
grep -v '^[[:space:]]*\(#\|$\)' debian/patches/series | while read -r p; do
	patch -p1 --forward --no-backup-if-mismatch <"debian/patches/$p"
done

prefix=/usr/lib/jellyfin-ffmpeg
set -- --prefix="$prefix" --bindir="$prefix" --libdir="$prefix/lib" \
	--extra-ldflags="-Wl,-rpath,$prefix/lib" \
	--target-os=linux --extra-version=Jellyfin \
	--disable-doc --disable-ffplay --disable-static --disable-libxcb \
	--disable-sdl2 --disable-xlib --enable-lto=auto \
	--enable-gpl --enable-version3 --enable-shared \
	--enable-gmp --enable-gnutls --enable-opencl --enable-libdrm \
	--enable-libxml2 --enable-libass --enable-libfreetype --enable-libfribidi \
	--enable-libfontconfig --enable-libharfbuzz --enable-libbluray \
	--enable-libmp3lame --enable-libopus --enable-libtheora --enable-libvorbis \
	--enable-libopenmpt --enable-libdav1d --enable-libsvtav1 --enable-libwebp \
	--enable-libvpx --enable-libx264 --enable-libx265 --enable-libzvbi \
	--enable-libzimg \
	--enable-ffnvcodec --enable-cuda --enable-cuda-llvm --enable-cuvid \
	--enable-nvdec --enable-nvenc
if [ "$target_arch" = amd64 ]; then
	set -- "$@" --arch=amd64 --enable-vaapi --enable-amf --enable-libvpl
else
	set -- "$@" --arch=aarch64
fi
if [ -n "$cross" ]; then
	set -- "$@" --enable-cross-compile --cross-prefix="$cross" --pkg-config=pkg-config
	export PKG_CONFIG_LIBDIR="/usr/lib/$triplet/pkgconfig:/usr/share/pkgconfig"
fi
export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig
./configure "$@"
make -j"$(nproc)"
make install DESTDIR="$out/root"
rm -rf "$out/root$prefix/include" "$out/root$prefix/lib/pkgconfig" "$out/root$prefix/share"
"${cross}strip" --strip-unneeded "$out/root$prefix/lib/"*.so.*
config="$(sed -n 's/^#define FFMPEG_CONFIGURATION "\(.*\)"$/\1/p' config.h)"
case " $config " in
	*" --enable-nonfree "* | *libfdk*)
		echo "build.sh: configuration is nonfree or links libfdk-aac: $config" >&2
		exit 1
		;;
esac

# Debian packages behind every shared object the binaries load, followed
# through NEEDED entries, plus the hardware runtimes above, then closed over
# Depends: a runtime loads some libraries with dlopen (intel-opencl-icd the
# graphics compiler), and apt installs every dependency in the image anyway.
cd "$work"
owner() { # soname -> "binary:Package" of the package that ships it
	for d in "/usr/lib/$triplet" "/lib/$triplet"; do
		for f in "$d/$1" "$(readlink -f "$d/$1" 2>/dev/null || true)"; do
			[ -e "$f" ] || continue
			for q in "$f" "/usr${f#/usr}" "${f#/usr}"; do
				pkg="$(dpkg -S "$q" 2>/dev/null | sed -n '1s/: .*//p')" || true
				if [ -n "$pkg" ]; then
					printf '%s %s\n' "${pkg%%,*}" "$(readlink -f "$f")"
					return 0
				fi
			done
		done
	done
	echo "build.sh: no Debian package ships $1" >&2
	return 1
}
needed() { "${cross}readelf" -d "$1" | sed -n 's/.*(NEEDED).*\[\(.*\)\]$/\1/p'; }
: >queue
: >seen
: >pkgs
for f in "$out/root$prefix/ffmpeg" "$out/root$prefix/ffprobe" "$out/root$prefix/lib/"*.so.*; do
	needed "$f" >>queue
done
for p in $hw_runtime; do
	dpkg -L "$p" | grep '\.so' | while read -r f; do
		if [ -f "$f" ] && ! [ -L "$f" ]; then
			needed "$f" >>queue
		fi
	done
	printf '%s\n' "$p" >>pkgs
done
while [ -s queue ]; do
	soname="$(sed -n 1p queue)"
	sed -i 1d queue
	grep -qxF "$soname" seen && continue
	printf '%s\n' "$soname" >>seen
	[ -e "$out/root$prefix/lib/$soname" ] && continue
	found="$(owner "$soname")"
	printf '%s\n' "${found%% *}" >>pkgs
	needed "${found#* }" >>queue
done
# The closure is resolved against dpkg rather than `apt-cache depends
# --installed`, which drops a target-architecture package's dependency on an
# Architecture: all package when cross compiling (that package is installed
# under the build architecture): libfontconfig1:arm64 lost fontconfig-config.
inst() { # dependency name -> installed package, target architecture first
	for q in "${1%%:*}:$target_arch" "${1%%:*}"; do
		if dpkg-query -W -f '${Status}\n' "$q" 2>/dev/null | grep -q ' installed$'; then
			printf '%s\n' "$q"
			return 0
		fi
	done
	# A virtual name (libva-driver-abi-1.17) resolves to the installed package
	# that Provides it, target architecture first.
	dpkg-query -W -f '${db:Status-Status} ${Package}:${Architecture} ${Provides}\n' |
		awk -v n="${1%%:*}" -v a="$target_arch" '
			$1 != "installed" { next }
			{ line = $0; sub(/^[^ ]* [^ ]* /, "", line); gsub(/\([^)]*\)/, "", line)
			  k = split(line, v, /[ ,]+/)
			  for (i = 1; i <= k; i++) if (v[i] == n) { if ($2 ~ (":" a "$")) { print $2; exit } else if (!o) o = $2 } }
			END { if (o) print o }' | sed -n 1p | grep .
}
deps_of() { # installed package -> the installed package satisfying each Pre-Depends/Depends
	dpkg-query -W -f '${Pre-Depends},${Depends}\n' "$1" | tr ',' '\n' | while read -r group; do
		[ -n "$group" ] || continue
		r=""
		# shellcheck disable=SC2046
		for alt in $(printf '%s\n' "$group" | sed 's/([^)]*)//g; s/|/ /g'); do
			r="$(inst "$alt")" && break
		done
		if [ -z "$r" ]; then
			echo "build.sh: $1 depends on '$group', which is not installed" >&2
			return 1
		fi
		printf '%s\n' "$r"
	done
}
sort -u pkgs >queue
: >closed
while [ -s queue ]; do
	p="$(sed -n 1p queue)"
	sed -i 1d queue
	q="$(inst "$p")" || { echo "build.sh: $p is not installed" >&2; exit 1; }
	grep -qxF "$q" closed && continue
	printf '%s\n' "$q" >>closed
	deps_of "$q" >>queue
done
while read -r q; do
	dpkg-query -W -f '${Package}=${Version} ${source:Package}=${source:Version}\n' "$q"
done <closed | sort -u >packages.txt
cut -d' ' -f1 packages.txt >"$out/runtime-packages.txt"

# Corresponding source of every Debian package above at its exact version, as
# Debian publishes it (.dsc with checksums, upstream and Debian tarballs).
# glibc and GCC's runtime libraries are System Libraries under GPL-3.0 s1 and
# are not part of the offer.
cut -d' ' -f2 packages.txt | sort -u | grep -v '^\(glibc\|gcc-[0-9]*\)=' >sources.txt
(cd "$src_out/debian" && xargs -r apt-get source --download-only <"$work/sources.txt")

cp "$here/build.sh" "$here/apt-snapshot.sh" "$lock" "$pin" "$src_out/build/"
{
	echo "# ffmpeg $version for linux/$target_arch, built by build/build.sh."
	echo "# configure: $config"
	echo "# binary package=version  source package=version"
	cat packages.txt
} >"$src_out/PACKAGES.txt"
(cd "$src_out" && find upstream debian build PACKAGES.txt -type f -print | LC_ALL=C sort | xargs sha256sum >SHA256SUMS)

bundle="beamfall-ffmpeg-$(sed -n 's/^FFMPEG_SOURCE_URL=.*\/v\(.*\)\.tar\.gz$/\1/p' "$lock")-linux-$target_arch-source.tar"
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
	-cf "$out/$bundle" -C "$src_out" SHA256SUMS PACKAGES.txt upstream debian build
(cd "$out" && sha256sum "$bundle" >SHA256SUMS)
rm -rf "$work" "$src_out"
