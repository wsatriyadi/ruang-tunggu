# PRD — YouTube Live Server

**Versi:** 1.0 — rancangan awal siap dijadikan acuan pengembangan  
**Tanggal:** 5 Oktober 2026  
**Pemilik kebutuhan:** Wisnu Satriyadi  
**Jenis produk:** aplikasi web untuk mengelola siaran YouTube dari video yang tersimpan di server  
**Referensi visual:** `photo_2026-10-05_23-22-15.jpg`

## 1. Ringkasan produk

YouTube Live Server adalah aplikasi web yang memungkinkan pengguna mengunggah video, menyusunnya menjadi playlist, mengatur urutan dan pengulangan pemutaran, menjadwalkan siaran, serta menjalankan streaming ke YouTube melalui server sendiri. Pengguna mengoperasikan aplikasi melalui browser desktop maupun ponsel.

Server menjalankan proses streaming secara mandiri. Menutup browser, mematikan komputer yang digunakan untuk membuka panel, atau keluar dari akun tidak menghentikan siaran.

Desain mengambil pendekatan dashboard gelap dengan sidebar kiri, kartu pemantauan server di bagian atas, pengelolaan media di area utama, dan preview serta kontrol siaran di sisi kanan.

**Batas interpretasi:** gambar menunjukkan antarmuka, bukan implementasi di belakangnya. Rincian perilaku, keamanan, teknologi, angka target, dan pemulihan dalam PRD ini merupakan spesifikasi usulan. Nama video, alamat IP, tanggal, dan angka statistik dalam gambar adalah contoh tampilan dan tidak boleh dijadikan data tetap aplikasi.

## 2. Tujuan dan pengguna

### 2.1 Tujuan

- Mengoperasikan siaran berbasis video rekaman tanpa menulis perintah terminal setiap kali akan live.
- Menjalankan playlist berulang untuk siaran panjang, dengan penghentian manual atau berdasarkan jadwal.
- Memudahkan pengguna mengetahui video yang sedang dikirim, kondisi server, dan penyebab kegagalan.
- Mengutamakan penggunaan sumber daya yang ringan untuk perangkat seperti STB Armbian, sambil tetap dapat dipasang pada server Linux yang lebih kuat.

### 2.2 Pengguna utama

Pemilik channel atau operator yang memiliki video siap tayang, akses ke server, dan konfigurasi siaran dari YouTube Studio. Contoh penggunaan adalah playlist musik relaksasi, pemandangan, atau program rekaman.

MVP menggunakan **satu akun administrator**, **satu server**, **satu tujuan streaming yang dikonfigurasi**, dan **maksimal satu sesi streaming aktif**. Ini keputusan awal rancangan berdasarkan tampilan referensi, bukan batas permanen arsitektur.

### 2.3 Indikator keberhasilan

- Operator dapat menjalankan satu siaran dari file yang telah siap tanpa terminal.
- Penjadwalan tetap bekerja ketika tidak ada browser yang membuka panel.
- Tidak muncul dua proses publisher untuk satu sesi akibat klik berulang atau scheduler yang dijalankan ulang.
- Uji siaran 24 jam dengan media kompatibel dan jaringan stabil selesai tanpa crash aplikasi atau pertumbuhan memori terus-menerus.
- Setiap kegagalan memiliki status, waktu, dan alasan yang dapat ditindaklanjuti.

## 3. Cakupan rilis

| Prioritas | Cakupan |
|---|---|
| P0 — wajib MVP | Login admin, dashboard, monitoring server, unggah dan kelola video, playlist, tiga mode pemutaran, loop, jadwal sekali jalan, start/stop/restart, reconnect, preview, log, pengaturan dan pemulihan service |
| P1 — setelah MVP | Konversi video sebelum siaran, jadwal berulang, ekspor/impor konfigurasi, dukungan service Windows, beberapa akun operator |
| P2 — pengembangan lanjutan | Beberapa channel/sesi bersamaan, pengelolaan broadcast melalui OAuth YouTube, metrik penonton dan chat, notifikasi eksternal |

MVP mengirim media menggunakan alamat RTMP/RTMPS dan stream key. Pembuatan acara YouTube, judul, deskripsi, privasi, thumbnail acara, dan pengaturan publikasi dilakukan di YouTube Studio. Playlist dalam aplikasi adalah antrean media lokal; tidak otomatis menjadi playlist YouTube.

Login Google, pembayaran, upload video ke channel sebagai video biasa, kamera live, efek visual, overlay, dan transcoding realtime tidak termasuk P0.

## 4. Pemetaan gambar menjadi antarmuka

| Bagian yang terlihat | Isi pada referensi | Kebutuhan aplikasi |
|---|---|---|
| Header | Nama aplikasi, informasi perangkat, Server Online, IP, jam, ikon pengaturan | Identitas instance, kondisi koneksi panel, alamat server yang relevan, waktu beserta zona waktu, pintasan Settings |
| Sidebar | Dashboard, Video Library, Playlist, Schedule, Live Control, Logs, Settings | Navigasi utama dengan penanda halaman aktif |
| Kartu CPU | Persentase dan grafik kecil | Penggunaan CPU host dengan riwayat pendek |
| Kartu RAM | Penggunaan dibanding kapasitas | RAM terpakai, total, dan persentase |
| Kartu suhu | Temperatur dan indikator | Pembacaan sensor; tampilkan “Tidak tersedia” jika perangkat tidak mendukung |
| Kartu jaringan | Network Upload dan grafik | Trafik keluar interface yang dipilih, dalam Mbps |
| Kartu streaming | Status LIVE dan timer | Status publisher lokal dan durasi sesi; status YouTube ditampilkan terpisah bila tersedia |
| Upload Video | Drag-and-drop, pilih file, upload | Unggah file dengan antrean dan kemajuan per file |
| Video Library | Thumbnail, nama, durasi, resolusi, ukuran, status, aksi | Daftar media dengan pencarian, preview, detail dan hapus |
| Playlist | Pilihan playlist, daftar berurutan, mode pemutaran, loop | CRUD playlist, pengurutan, pilihan mode dan pengulangan |
| Live Schedule | Playlist, waktu mulai/selesai, opsi dan daftar jadwal | Membuat dan memantau jadwal siaran |
| Live Preview | Gambar/video serta metadata media aktif | Embed YouTube opsional, informasi file aktif, dan fallback thumbnail |
| Stream Control | Start Live, Stop Live, Restart Stream | Kontrol sesi dengan status proses dan perlindungan klik ganda |
| Log FFmpeg | Log realtime dan Clear Log | Log yang dapat difilter; membersihkan tampilan tanpa menghapus riwayat |
| System | SoC, RAM, OS, kernel, uptime | Inventaris perangkat yang dideteksi otomatis |

Tombol detail video merupakan interpretasi fungsi ikon pada gambar. Halaman yang hanya terlihat melalui sidebar perlu dirancang sebagai tampilan lengkap dari modul terkait.

## 5. Rancangan UI/UX

### 5.1 Gaya visual

- Tema gelap: latar navy hampir hitam, kartu biru gelap, garis tepi tipis, dan bayangan lembut.
- Palet awal usulan: latar `#07121D`, kartu `#122235`, aksen biru `#168BFF`, hijau `#22C55E`, merah `#EF233C`, teks utama `#F1F5F9`.
- Sudut kartu 10–14 px, jarak antarkomponen 12–16 px, tipografi sans-serif yang jelas, dan angka timer memakai lebar karakter tetap.
- Biru untuk navigasi/aksi umum, hijau untuk siap/mulai, merah untuk berhenti/gagal, kuning untuk peringatan.
- Status harus memiliki label teks atau ikon sehingga tidak hanya dibedakan melalui warna.
- Gunakan nama dan identitas visual milik aplikasi. Ikon tujuan YouTube dapat berada pada bagian integrasi.

### 5.2 Tata letak

Desktop lebar menggunakan sidebar sekitar 180–220 px. Area konten mempunyai baris statistik, lalu tiga kolom: upload dan playlist di kiri, library dan jadwal di tengah, preview, kontrol, dan log di kanan. Proporsi awal kolom adalah 3:5:3 dan menyesuaikan ruang.

Dashboard menampilkan versi ringkas setiap modul. Halaman Video Library, Playlist, Schedule, dan Logs menyediakan ruang kerja penuh untuk data yang lebih banyak.

Pada tablet, kartu menggunakan dua kolom dan sidebar dapat diperkecil. Pada ponsel, sidebar menjadi drawer, konten satu kolom, dan status serta kontrol sesi ditempatkan dekat bagian atas. Tabel video berubah menjadi kartu atau area scroll lokal; seluruh halaman tidak boleh melebar keluar viewport.

Semua tombol ikon memiliki label bantuan. Area sentuh ditargetkan minimal 44 × 44 px. Keyboard dapat mengakses kontrol utama; pengurutan playlist juga menyediakan tombol naik/turun sebagai alternatif drag-and-drop.

### 5.3 Keadaan tampilan

Setiap modul memiliki keadaan memuat, kosong, berhasil, dan gagal. Contoh: “Belum ada video. Unggah video pertama”, “Sambungan ke server terputus”, dan “File ini belum kompatibel dengan profil siaran”.

Saat panel kehilangan koneksi, tampilkan waktu pembaruan terakhir dan tandai statistik sebagai kedaluwarsa. Hilangnya koneksi browser tidak boleh langsung dianggap sebagai berhentinya streaming.

## 6. Alur pengguna utama

### 6.1 Persiapan pertama

1. Administrator membuka aplikasi dan membuat kredensial awal melalui proses setup yang hanya dapat dijalankan sekali.
2. Memilih nama server, zona waktu, lokasi penyimpanan, dan interface jaringan untuk monitoring.
3. Memasukkan URL ingest RTMPS/RTMP, stream key, dan opsional URL/video ID untuk preview YouTube.
4. Aplikasi memeriksa keberadaan FFmpeg/FFprobe, akses direktori, ruang disk, dan validitas konfigurasi.
5. Administrator menyiapkan broadcast di YouTube Studio, termasuk perilaku mulai/selesai otomatis sesuai kebutuhan.

### 6.2 Mulai siaran manual

1. Unggah video dan tunggu pemeriksaan media.
2. Buat playlist, masukkan video, tentukan urutan/mode dan loop.
3. Pilih playlist di Live Control dan tekan Start Live.
4. Aplikasi melakukan preflight; jika lolos, worker menjalankan publisher.
5. Operator memantau status pengiriman, file aktif, timer, log, dan preview.
6. Stop Live menghentikan publisher secara terkendali.

### 6.3 Siaran terjadwal

1. Pilih playlist dan revisinya, isi tanggal/jam mulai serta selesai, lalu tentukan mode, loop, dan reconnect.
2. Aplikasi memvalidasi media, rentang waktu, dan benturan jadwal.
3. Simpan dan aktifkan jadwal.
4. Scheduler server memulai sesi pada waktunya meskipun browser ditutup.
5. Pada waktu selesai, sesi dihentikan dan riwayatnya disimpan.

## 7. Persyaratan fungsional

### FR-01 — Autentikasi dan akses

- Semua halaman pengelolaan, API, media lokal, dan event realtime memerlukan autentikasi.
- Sediakan login, logout, dan perubahan kata sandi. MVP tidak memiliki registrasi publik.
- Setup awal menggunakan token sekali pakai atau alur lokal yang melindungi pembuatan akun pertama.
- Login diberi pembatasan percobaan. Sesi kadaluwarsa tidak menghentikan worker.
- Password disimpan sebagai hash adaptif. Cookie sesi menggunakan HttpOnly, SameSite, dan Secure pada HTTPS.

### FR-02 — Dashboard dan monitoring

- Tampilkan CPU, RAM, suhu, upload jaringan, kondisi streaming, dan uptime.
- Tambahkan informasi disk terpakai/tersedia karena video dapat menghabiskan penyimpanan.
- Sampling awal setiap 2 detik; riwayat grafik pendek 15 menit dengan buffer terbatas.
- Pisahkan trafik upload seluruh interface dari bitrate publisher; keduanya tidak boleh diberi label sebagai data yang sama.
- Pisahkan uptime server, durasi sesi, durasi koneksi publisher saat ini, serta posisi video.
- Informasi OS, kernel, CPU/SoC dan arsitektur berasal dari perangkat. Nilai yang gagal dibaca ditampilkan sebagai tidak tersedia.
- Statistik dikirim ke browser melalui SSE atau mekanisme realtime setara tanpa refresh halaman.

### FR-03 — Upload dan Video Library

**Input dan proses upload**

- Terima MP4, MOV, dan MKV sebagai format impor awal. Ekstensi yang diterima tidak berarti semua codec di dalamnya dapat disiarkan.
- Dukung pilih beberapa file dan drag-and-drop. Default antrean satu upload aktif untuk menjaga beban perangkat kecil.
- Tampilkan ukuran, persentase, byte terkirim, status, serta aksi batal/coba lagi.
- Gunakan upload bertahap ke disk. Ukuran potongan awal 8 MiB; file besar tidak dimuat seluruhnya ke RAM.
- Simpan ID upload agar transfer yang terputus dapat dilanjutkan. Potongan kedaluwarsa dibersihkan setelah 24 jam, selama upload tidak aktif.
- Batas ukuran file, kuota media, dan cadangan disk dapat dikonfigurasi. Default cadangan disk adalah nilai terbesar antara 1 GiB dan 5% kapasitas volume.
- Pemeriksaan ruang dilakukan sebelum dan selama upload dengan memperhitungkan transfer lain. Kehabisan ruang menghasilkan error yang jelas dan tidak membuat entri siap pakai.

**Pemeriksaan media**

- Setelah upload lengkap, FFprobe mengekstrak durasi, ukuran, container, codec, resolusi, frame rate, bitrate yang tersedia, audio sample rate, channel, dan time base.
- Thumbnail dibuat sebagai pekerjaan terpisah dengan antrean terbatas. Gagal membuat thumbnail tidak harus menggagalkan media yang valid.
- Status penyimpanan: `uploading`, `processing`, `ready`, `invalid`, atau `missing`.
- Status kompatibilitas siaran ditampilkan terpisah: `unchecked`, `compatible`, `incompatible`, atau `needs_review` beserta alasan.
- `ready` berarti file tersedia dan pemeriksaan dasar berhasil; bukan jaminan file sepenuhnya bebas kerusakan atau sudah sesuai untuk semua playlist.
- Pemeriksaan ringan bukan pemindaian seluruh frame. Error yang baru muncul ketika media diputar tetap harus ditangani worker.

**Pengelolaan**

- Tabel memuat thumbnail, nama, durasi, resolusi, ukuran, status, dan aksi preview/detail/hapus; tersedia pencarian serta pagination.
- Preview lokal menggunakan dukungan browser. Bila codec/container tidak didukung browser, tampilkan thumbnail dan penjelasan; jangan membuat transcoding realtime otomatis.
- Nama file asli hanya untuk tampilan; lokasi penyimpanan menggunakan ID internal yang aman.
- File yang dipakai sesi aktif atau masih direferensikan playlist/revisi jadwal tidak dapat dihapus. Tampilkan daftar pemakaiannya.
- Penghapusan file yang tidak dipakai memerlukan konfirmasi. Tidak ada penghapusan otomatis setelah siaran dalam MVP.

### FR-04 — Playlist

- Buat, ubah nama, lihat, dan hapus playlist yang tidak memiliki dependensi aktif.
- Tambah/hapus media, ubah urutan dengan drag-and-drop atau tombol naik/turun.
- Tampilkan jumlah video, durasi satu putaran, dan hasil pemeriksaan kompatibilitas.
- Video yang sama hanya boleh muncul sekali dalam satu revisi playlist pada MVP; pengulangan dilakukan melalui mode pemutaran/loop.
- Setiap penyimpanan menghasilkan revisi playlist. Sesi aktif memakai snapshot yang tidak berubah ketika playlist diedit.
- Jadwal mengacu ke revisi yang dipilih saat disimpan. Tampilkan penanda bila revisi lebih baru tersedia; pembaruan jadwal dilakukan secara eksplisit.

| Mode | Perilaku yang wajib |
|---|---|
| Sequential | Memutar semua video sesuai urutan playlist |
| Random | Memilih video secara acak dengan kemungkinan pengulangan; satu putaran berisi N pemilihan, dengan N = jumlah video dalam playlist |
| Random tanpa pengulangan | Mengacak seluruh N video dan memutar masing-masing satu kali dalam satu putaran |

Loop adalah opsi terpisah dari mode. Jika loop mati, sesi selesai setelah satu putaran. Jika loop hidup, putaran berikutnya dibuat sampai operator menghentikan sesi atau waktu akhir tercapai. Untuk random tanpa pengulangan, urutan diacak ulang setiap putaran; bila N lebih dari satu, hindari video terakhir suatu putaran langsung menjadi video pertama putaran berikutnya.

Urutan aktual, nomor putaran, dan item aktif disimpan agar riwayat dapat diperiksa dan pemulihan tidak mengacak ulang seluruh antrean secara tidak terduga.

### FR-05 — Schedule

**Field:** nama jadwal, playlist/revisi, tanggal dan jam mulai, tanggal dan jam selesai, zona waktu, mode pemutaran, loop, auto reconnect, dan status aktif.

- MVP mendukung jadwal sekali jalan. Mulai dan selesai wajib diisi, termasuk tanggal yang berbeda untuk siaran melewati tengah malam.
- Contoh zona waktu awal untuk pengguna di Kalimantan Selatan: `Asia/Makassar` / WITA. Tetap dapat diubah saat setup.
- Waktu disimpan sebagai UTC dengan zona waktu IANA untuk tampilan. Perubahan zona waktu tampilan tidak menggeser jadwal yang sudah disimpan.
- Waktu selesai harus lebih besar dari waktu mulai. Jadwal baru dengan waktu mulai yang telah lewat ditolak.
- Jadwal aktif yang bertumpang tindih pada kapasitas satu sesi ditolak saat simpan maupun saat diaktifkan. Interval menggunakan `[mulai, selesai)` sehingga dua jadwal yang bersambung tepat waktu dapat dibuat.
- Pada pergantian jadwal, sesi lama harus benar-benar berhenti sebelum sesi baru memperoleh slot publisher.
- Scheduler berjalan di server dengan klaim eksekusi atomik dan identitas eksekusi unik, bukan timer browser.
- Jika sesi manual masih aktif saat jadwal tiba, jadwal menjadi `skipped_busy`. Aplikasi tidak mematikan sesi manual secara diam-diam dan tidak menjalankan jadwal tersebut terlambat tanpa tindakan pengguna.
- Loop mati dapat menyebabkan sesi selesai sebelum waktu akhir. UI memberi penjelasan ini saat menyimpan jadwal.
- Pada waktu akhir, sesi berhenti meskipun video belum selesai. Tidak ada opsi melanjutkan video melewati batas waktu dalam P0.
- Menonaktifkan jadwal mencegah pemicuan berikutnya, tetapi tidak menghentikan sesi yang sudah berjalan. Penghentian sesi menggunakan Stop Live.
- Detail jadwal yang sedang berjalan tidak dapat diubah atau dihapus sampai sesi selesai. Toggle aktif tetap dapat dimatikan, dengan perilaku yang dijelaskan di atas.

Status jadwal: `disabled`, `scheduled`, `running`, `completed`, `stopped`, `failed`, `missed`, dan `skipped_busy`.

### FR-06 — Live Control dan engine

**Kontrol utama**

- Start Live memilih playlist dan konfigurasi sesi, lalu meminta worker melakukan preflight sebelum menjalankan publisher.
- Stop Live menetapkan niat berhenti terlebih dahulu, membatalkan antrean reconnect, lalu menghentikan proses. Stop harus berlaku juga ketika sesi sedang starting/reconnecting.
- Restart Stream menghentikan attempt lama dan membuat attempt baru pada sesi yang sama setelah proses lama terverifikasi selesai.
- Stop dan Restart pada sesi aktif menampilkan konfirmasi singkat. Tombol yang sedang diproses dinonaktifkan.
- Klik berulang, tab ganda, dan request bersamaan menghasilkan satu tindakan efektif melalui idempotency key dan lock sesi.
- Menutup tab, logout, atau melakukan reload halaman tidak memengaruhi proses di server.

**Preflight sebelum mulai**

Periksa kredensial tujuan, executable yang diperlukan, tidak adanya publisher aktif lain, keberadaan dan akses file, isi playlist, hasil kompatibilitas, akses penyimpanan, serta waktu akhir yang masih berlaku. Kegagalan harus menjelaskan file atau konfigurasi mana yang perlu diperbaiki.

**Kebijakan media**

MVP memprioritaskan pengiriman H.264 + AAC yang sudah dipersiapkan dengan mode stream copy. FFmpeg stream copy memindahkan paket tanpa decode/encode; filter visual memerlukan jalur pemrosesan lain. Untuk input concat, stream antarfile harus sesuai, termasuk codec dan time base. Karena itu, pemeriksaan playlist tidak boleh hanya membandingkan ekstensi atau resolusi. [S1][S2]

Aturan produk untuk profil awal adalah satu video dan satu audio per file, parameter video/audio yang seragam, serta hasil validasi playlist yang lolos. Berkas dengan kombinasi parameter berbeda atau tanpa audio perlu dipersiapkan lebih dahulu. Sistem tidak diam-diam memulai transcoding berat pada perangkat kecil.

Pilihan profil awal adalah SDR H.264/AAC dan RTMPS. Pemeriksaan keyframe menggunakan acuan rekomendasi YouTube 2 detik dan batas 4 detik; angka bitrate mengikuti profil resolusi/frame rate yang dipilih dan dokumentasi saat implementasi. Mengubah bitrate, frame rate, resolusi, atau jarak keyframe tidak menjadi kontrol bebas ketika sesi memakai copy. [S1][S3]

Pergantian item dan putaran harus mempertahankan timeline media yang valid. Jangan mengandalkan restart koneksi tujuan setiap kali berganti file sebagai mekanisme playlist. Strategi penggantian sumber, pengacakan putaran baru, dan timestamp harus dibuktikan dalam prototipe engine; ini gate kelulusan P0. PRD tidak menjanjikan perpindahan tanpa celah untuk semua file arbitrer.

**Status sesi**

| Status | Makna |
|---|---|
| IDLE | Tidak ada sesi aktif |
| VALIDATING | Menjalankan pemeriksaan dan memesan slot sesi |
| STARTING | Publisher sedang disiapkan atau membuka koneksi |
| STREAMING | Publisher hidup dan menunjukkan kemajuan pengiriman |
| RECONNECTING | Koneksi/proses gagal dan sedang menunggu atau mencoba pemulihan |
| STOPPING | Publisher sedang dihentikan |
| STOPPED | Sesi dihentikan operator |
| COMPLETED | Playlist tanpa loop habis atau waktu akhir tercapai |
| FAILED | Kegagalan terminal; diperlukan tindakan operator |

Nama status ini berlaku untuk server lokal. UI tidak menyimpulkan siaran sudah tayang di YouTube hanya dari proses yang hidup. YouTube membedakan status penerimaan stream dan status broadcast. Tanpa integrasi API, tampilkan “Mengirim ke YouTube — status tayang belum diverifikasi” serta tautan ke YouTube Studio. [S4][S5]

### FR-07 — Reconnect dan pemulihan

- Worker memantau exit code, kemajuan output, dan kondisi macet. Default kemajuan yang tidak berubah selama 30 detik memicu pemeriksaan/pemulihan; nilai dapat diatur.
- Retry otomatis berlaku untuk gangguan yang diperkirakan sementara. Penolakan autentikasi yang jelas, konfigurasi salah, atau seluruh playlist tidak dapat dibaca menghasilkan FAILED.
- Default jeda retry: 2, 5, 10, 20, lalu maksimal 30 detik, dengan jitter kecil; maksimal 10 percobaan berturut-turut. Counter direset setelah pengiriman stabil selama 2 menit.
- UI menampilkan alasan gangguan, nomor percobaan, dan waktu retry berikutnya.
- Reconnect dan Restart MVP memulai ulang file yang sedang aktif dari awal. Posisi tepat tidak dijanjikan; simpan kejadian pengulangan dalam log.
- Kegagalan satu file saat playback menandai item bermasalah dan mencoba item valid berikutnya. Jika semua item gagal dalam satu putaran, sesi menjadi FAILED untuk mencegah loop error tanpa akhir.
- Checkpoint menyimpan sesi, niat berjalan/berhenti, item, putaran, urutan, dan batas waktu. Pencatatan posisi periodik dapat dibatasi setiap 10 detik untuk mengurangi penulisan disk.
- Service mulai otomatis saat boot. Pengaturan `resume_after_restart` menentukan apakah sesi yang terputus dipulihkan; usulan default aktif.
- Sesi yang sebelumnya dihentikan operator tidak boleh hidup kembali akibat reboot atau retry.
- Untuk jadwal yang sudah lewat waktu selesai, tandai `missed` bila belum dijalankan, atau tutup sesi sebelumnya dengan alasan pemulihan setelah batas waktu. Jangan memutar rangkaian jadwal lama.
- Jika server kembali ketika rentang jadwal masih berjalan, resume hanya jika pengaturan pemulihan aktif dan slot tersedia. Jika pemulihan nonaktif, gunakan status `missed` dengan alasan pemulihan dinonaktifkan; jika slot terpakai sesi lain, gunakan `skipped_busy`.
- Rekonsiliasi saat startup memverifikasi proses milik worker sebelum memulai pengganti. Hanya satu worker memiliki kendali publisher.

### FR-08 — Preview dan informasi media aktif

- Tampilkan nama file aktif, thumbnail, durasi total, posisi pemutaran, nomor item/putaran, resolusi, FPS, dan codec.
- Data posisi berasal dari kemajuan publisher dan timeline playlist, bukan hanya jam browser. Setelah pemulihan, posisi mengikuti file yang dimulai ulang.
- Embed YouTube menggunakan video ID atau URL yang dimasukkan administrator dan tetap bergantung pada izin pemutaran/embed dari YouTube.
- Jika preview YouTube tidak tersedia, tampilkan thumbnail lokal dan tombol membuka siaran/YouTube Studio.
- Beri label yang jelas: “Preview YouTube”, “Preview file lokal”, atau “Thumbnail video aktif”. Thumbnail statis tidak boleh disajikan sebagai gambar live realtime.
- Preview YouTube dapat tertinggal dari pengiriman server; panel tidak menjanjikan keduanya sinkron frame demi frame.
- Jangan membuat stream preview tambahan secara otomatis pada server 2 GB. Pembuatan HLS preview lokal merupakan opsi lanjutan, bila dibutuhkan dan sumber daya telah diuji.

### FR-09 — Log dan riwayat

- Pisahkan log aplikasi, scheduler, publisher, dan audit tindakan operator.
- Tampilkan timestamp, level, sesi, sumber, dan pesan. Gunakan zona waktu yang dipilih pada tampilan.
- Dukung filter level/sumber/sesi, pencarian, pause auto-scroll, dan unduh log yang telah disanitasi.
- Clear Log hanya mengosongkan tampilan sesi browser. Retensi dan penghapusan riwayat dilakukan dari Settings dengan aturan terpisah.
- Rotasi log berdasarkan ukuran dan usia. Default usulan total 100 MiB dan 7 hari; hapus berkas tertua ketika salah satu batas tercapai.
- Stream key, token sesi, dan password harus disensor sebelum masuk log, event realtime, maupun file ekspor.
- Log mencatat siapa yang mulai/stop/restart, jadwal yang memicu sesi, perubahan pengaturan, dan alasan sesi berakhir.

### FR-10 — Settings

| Kelompok | Pengaturan |
|---|---|
| Umum | Nama instance, bahasa awal Indonesia, zona waktu |
| Tujuan siaran | URL RTMPS/RTMP, stream key tersamarkan, URL/video ID preview |
| Media | Direktori penyimpanan, kuota, batas upload, cadangan disk |
| Engine | Profil media, lokasi FFmpeg/FFprobe, kebijakan reconnect dan pemulihan |
| Monitoring | Interface jaringan, interval pembaruan, ambang peringatan |
| Log | Retensi ukuran/usia dan ekspor tersanitasi |
| Akun | Ubah password dan logout |

Perubahan tujuan siaran/profil berlaku untuk sesi berikutnya. Sesi berjalan menggunakan snapshot konfigurasi saat mulai. Perubahan lokasi media harus divalidasi dan ditolak ketika ada upload atau streaming aktif; pemindahan media otomatis bukan bagian MVP.

## 8. Hubungan aplikasi dengan YouTube

Stream key memberi aplikasi kemampuan mengirim media, bukan mengelola seluruh channel. Setup harus mengarahkan operator menyiapkan broadcast yang benar dan memeriksa pengaturan mulai/selesai otomatis di YouTube Studio. Penghentian publisher lokal tidak menjamin broadcast YouTube berakhir pada detik yang sama. [S5][S6]

RTMPS menjadi pilihan awal karena direkomendasikan YouTube untuk pengiriman terenkripsi. H.264/AAC adalah profil konservatif yang dipilih produk untuk MVP, bukan pernyataan bahwa YouTube hanya mendukung kedua codec tersebut. [S3]

Apabila integrasi API ditambahkan kemudian, gunakan identitas stream dan broadcast yang terikat untuk menampilkan status tayang serta kesehatan ingest. Kesalahan atau ketiadaan data API harus menghasilkan status tidak diketahui, bukan LIVE palsu. [S4][S5]

## 9. Persyaratan nonfungsional

Semua angka berikut adalah sasaran pengujian, belum merupakan hasil benchmark perangkat pada gambar.

| Area | Sasaran penerimaan |
|---|---|
| Respons panel | Respons API metadata/kontrol p95 di bawah 500 ms pada LAN; operasi media berat berjalan sebagai pekerjaan terpisah |
| Kesegaran data | Statistik baru tampil maksimal 5 detik setelah sampling dalam kondisi koneksi normal |
| Jadwal | Worker mengeluarkan perintah start/stop dalam toleransi 5 detik saat server sehat; waktu tampil di YouTube diukur terpisah |
| Stop | Publisher berhenti paling lambat 10 detik; graceful stop diikuti penghentian paksa hanya untuk proses milik sesi jika diperlukan |
| Stabilitas | Lulus uji 24 jam mode copy dengan playlist kompatibel, pergantian file, dan beberapa putaran |
| Memori | Target backend dan layanan pendukung aplikasi di bawah 250 MiB pada beban normal, di luar OS, cache filesystem dan proses media; keseluruhan host 2 GB tidak boleh mengalami OOM pada skenario uji yang disepakati |
| Upload | Memori tidak naik sebanding ukuran file; uji file 5 GB pada penyimpanan yang mencukupi |
| Perangkat | P0 Linux ARM64 dan x86-64; kompatibilitas CPU/binary FFmpeg dan sensor diverifikasi per instalasi |
| Browser | Tata letak bekerja pada desktop dan viewport ponsel mulai 360 px; diuji pada browser utama yang tersedia saat rilis |
| Pemulihan | Tidak ada publisher ganda setelah restart worker/service; manual stop tetap dihormati |

Perangkat S905X dengan RAM 2 GB adalah sasaran validasi mode copy. Kemampuan realtime transcoding, akselerasi perangkat keras, atau resolusi tertentu tidak diasumsikan hanya dari nama SoC. Kapasitas disk dan uplink tetap harus sesuai media yang digunakan.

## 10. Keamanan dan integritas data

- Jalankan service dengan user sistem khusus dan izin minimum, bukan root jika tidak diperlukan.
- URL tujuan dibatasi pada skema dan host ingest yang diizinkan; jangan meneruskan input pengguna sebagai perintah shell.
- Argumen proses dibentuk sebagai array terstruktur. Path lokal berasal dari ID media tervalidasi, bukan path bebas dari browser.
- Lindungi endpoint mutasi dari CSRF, escape nama file dalam UI, dan lakukan otorisasi untuk semua akses media serta log.
- Simpan stream key terenkripsi saat tersimpan, dengan kunci aplikasi terpisah dari database. API pengaturan hanya mengembalikan status “sudah diatur” atau nilai tersamarkan.
- Batasi akses OS ke konfigurasi, proses, dan log karena kredensial output dapat digunakan oleh proses streaming.
- Gunakan HTTPS saat panel diakses melalui jaringan yang tidak dipercaya. Konfigurasi awal harus menjelaskan alamat bind dan metode aksesnya.
- Tulisan upload menjadi file final secara atomik setelah lengkap. Bersihkan file sementara yang gagal dan cegah path traversal.
- Backup database secara konsisten dan dokumentasikan pemulihan konfigurasi, media, serta kunci enkripsi. Backup media berukuran besar dapat dikelola terpisah oleh administrator.

## 11. Arsitektur dan pilihan teknologi

Pilihan berikut merupakan usulan implementasi untuk perangkat ringan dan bukan teknologi yang dapat disimpulkan dari screenshot.

| Komponen | Pilihan awal | Tanggung jawab |
|---|---|---|
| Frontend | React + TypeScript + Vite, hasil build statis | Dashboard, formulir, playlist, status dan log |
| Backend | Go | API, autentikasi, upload, akses data dan event realtime |
| Worker | Komponen server dengan kepemilikan tunggal atas proses media | Scheduler, preflight, playlist, publisher dan pemulihan |
| Engine media | FFmpeg dan FFprobe | Probe, thumbnail, pembacaan media dan publishing |
| Database | SQLite | Pengguna, metadata, playlist, jadwal, sesi dan audit |
| File storage | Filesystem lokal atau volume terpasang | Video, thumbnail dan file sementara |
| Status realtime | Server-Sent Events | Metrik, perubahan status dan log ke browser |
| Pengelola service | systemd untuk Linux | Startup saat boot, restart service dan pembatasan hak akses |

Frontend dapat disajikan oleh backend agar instalasi awal tidak memerlukan banyak service. SQLite memakai transaksi singkat dan antrean penulisan yang terkontrol; media tidak disimpan sebagai blob database. Scheduler tidak memerlukan Redis dalam MVP.

Pisahkan modul API, media, playlist, schedule, streaming, monitoring, auth, dan audit dalam kode. Modul streaming mempunyai kontrak yang memungkinkan penggantian implementasi engine atau penambahan beberapa sesi pada rilis berikutnya.

Versi dependency dan build FFmpeg dikunci pada implementasi serta diuji pada kedua arsitektur sasaran. Dukungan Windows memerlukan adapter service, path, sensor, dan terminasi proses tersendiri pada P1.

## 12. Model data inti

| Entitas | Field inti / hubungan |
|---|---|
| users | id, username, password_hash, created_at |
| settings | nama instance, timezone, kebijakan upload/log/recovery, versi konfigurasi |
| stream_targets | id, nama, ingest_url, encrypted_stream_key, preview_video_id, profile_id, version |
| stream_profiles | codec dan parameter media yang diterima, aturan validasi, version |
| upload_sessions | id, user_id, expected_size, received_parts, status, expires_at |
| media_files | id, original_name, storage_path, size, duration, metadata, thumbnail_path, file_status |
| media_validations | media_id, profile_id/version, compatibility_status, issues, checked_at |
| playlists | id, name, latest_revision_id, created_at |
| playlist_revisions | id, playlist_id, revision_number, playback_mode, loop_enabled, created_at |
| playlist_items | revision_id, media_id, position; kombinasi revision_id/media_id unik |
| schedules | id, revision_id, target_id, start_at_utc, end_at_utc, timezone, mode, loop, reconnect, enabled, status |
| stream_sessions | id, schedule_id opsional, snapshot_config, desired_state, observed_state, start/end, stop_reason, owner_epoch |
| playback_items | session_id, cycle_number, item_index, media_id, expected_duration, playback_status |
| stream_attempts | id, session_id, process_identity, started_at, ended_at, exit_code, error_code |
| operation_requests | idempotency_key, action, session_id, result; mencegah tindakan ganda |
| audit_events | actor_id, action, entity_id, timestamp, metadata tersanitasi |

Log media yang banyak dapat berada dalam file berotasi. Database menyimpan indeks sesi dan ringkasan kejadian penting. Relasi mencegah penghapusan media/revisi yang masih dipakai; kebijakan pembersihan revisi lama dilakukan eksplisit setelah dependensinya selesai.

## 13. Kontrak API awal

Path berikut merupakan rancangan antarmuka backend, bukan API YouTube.

| Metode dan path | Fungsi |
|---|---|
| POST /api/auth/login, /logout | Kelola sesi pengguna |
| GET /api/dashboard | Ringkasan metrik, sesi, media aktif dan jadwal berikutnya |
| GET /api/events | SSE autentikasi untuk status, metrik, dan log |
| POST /api/uploads | Buat sesi upload |
| PUT /api/uploads/:id/parts/:part | Unggah potongan secara idempotent |
| GET /api/uploads/:id | Ambil potongan yang sudah tersimpan untuk melanjutkan upload |
| POST /api/uploads/:id/complete | Verifikasi kelengkapan dan jadwalkan pemeriksaan |
| DELETE /api/uploads/:id | Batalkan dan bersihkan upload |
| GET /api/videos; GET /api/videos/:id | Daftar/pencarian dan detail media |
| GET /api/videos/:id/content | Preview lokal dengan otorisasi dan dukungan byte range |
| DELETE /api/videos/:id | Hapus media setelah pemeriksaan referensi |
| GET/POST /api/playlists | Daftar/buat playlist |
| PUT/DELETE /api/playlists/:id | Buat revisi baru atau hapus playlist bila diizinkan |
| GET/POST /api/schedules | Daftar/buat jadwal |
| PATCH/DELETE /api/schedules/:id | Ubah status/detail atau hapus jadwal bila diizinkan |
| POST /api/stream/start, /stop, /restart | Perintah lifecycle dengan idempotency key |
| GET /api/stream/status | Status, posisi, antrean dan alasan kegagalan |
| GET /api/logs; GET /api/logs/export | Baca dan ekspor log tersanitasi |
| GET/PUT /api/settings | Baca pengaturan tersamarkan dan simpan pengaturan |

Setiap error mengembalikan kode stabil, pesan yang dapat dipahami, dan detail objek bermasalah tanpa kredensial. Contoh: `STREAM_BUSY`, `SCHEDULE_CONFLICT`, `MEDIA_INCOMPATIBLE`, `MEDIA_IN_USE`, `INSUFFICIENT_STORAGE`, `INVALID_TARGET`, dan `DEPENDENCY_MISSING`.

Start/restart dapat mengembalikan HTTP 202 dengan identitas operasi/sesi; perkembangan dikirim melalui SSE. Konflik resource mengembalikan 409. Tidak ada respons sukses palsu sebelum operasi dicatat secara persisten.

## 14. Penanganan kondisi khusus

| Kondisi | Perilaku wajib |
|---|---|
| FFmpeg tidak terpasang | Start dinonaktifkan; tampilkan dependensi yang perlu dipasang |
| Stream key salah | Gagal dengan pesan tersanitasi; hentikan retry bila penolakan teridentifikasi |
| Metadata video berbeda dalam playlist | Tampilkan file dan parameter yang tidak sesuai; blokir start sampai diperbaiki |
| Disk hampir penuh | Hentikan penerimaan upload baru dan tampilkan kebutuhan ruang; jangan menghapus media otomatis |
| File dihapus di luar aplikasi | Tandai missing, cegah start baru, dan tangani sesi aktif sesuai aturan item gagal |
| Browser terputus | Streaming tetap berjalan; panel melakukan reconnect dan mengambil snapshot status terbaru |
| Jaringan server terputus | Masuk RECONNECTING, tampilkan percobaan dan patuhi batas akhir jadwal |
| Jam server berubah | Rekonsiliasi deadline dengan waktu saat ini dan gunakan identitas eksekusi untuk mencegah jadwal dipicu ulang |
| Sensor suhu tidak tersedia | Tampilkan “Tidak tersedia”; fungsi lain tetap berjalan |
| YouTube embed diblokir | Tampilkan thumbnail dan tautan membuka YouTube |
| Stop bersamaan dengan retry | Niat stop menang; retry yang belum mulai dibatalkan |
| Service hidup kembali | Rekonsiliasi proses dan sesi persisten sebelum membuat publisher baru |

## 15. Acceptance criteria

| ID | Skenario dan hasil yang diharapkan |
|---|---|
| AC-01 | Akses tanpa login tidak dapat membaca API pengelolaan, video lokal, stream key, atau log |
| AC-02 | Unggah MP4 valid menampilkan progres, metadata, dan thumbnail; MOV/MKV diperiksa berdasarkan isinya |
| AC-03 | Upload file 5 GB pada disk yang mencukupi tidak menyebabkan penggunaan RAM sebesar file tersebut; transfer yang terputus dapat dilanjutkan |
| AC-04 | File tidak valid menampilkan alasan; kapasitas disk yang tidak mencukupi tidak menghasilkan media berstatus ready |
| AC-05 | Urutan playlist yang disimpan tetap sama setelah reload dan revisi lama tetap tersedia bagi jadwal yang memakainya |
| AC-06 | Sequential memutar sesuai urutan; random tanpa pengulangan memutar setiap video sekali per putaran dan membuat urutan baru pada putaran selanjutnya |
| AC-07 | Loop mati menyelesaikan satu putaran; loop hidup memulai putaran berikutnya; Stop selalu mengakhiri keduanya |
| AC-08 | Playlist kompatibel melewati pergantian file dan beberapa putaran tanpa restart koneksi tujuan yang disengaja per file; log tidak memuat error timestamp yang menyebabkan sesi gagal |
| AC-09 | Playlist tidak kompatibel diblokir dengan perbedaan parameter yang jelas; aplikasi tidak beralih ke transcoding realtime tanpa pilihan eksplisit |
| AC-10 | Dua request Start bersamaan, termasuk dari tab berbeda, hanya menghasilkan satu sesi dan satu publisher |
| AC-11 | Menutup browser selama streaming tidak menghentikan media; membuka ulang panel memperlihatkan status aktual |
| AC-12 | Jadwal lintas tengah malam dimulai dan dihentikan berdasarkan zona waktu yang dipilih dalam toleransi server yang ditetapkan |
| AC-13 | Jadwal bertumpang tindih ditolak, sedangkan jadwal bersambung tidak menyebabkan publisher ganda |
| AC-14 | Sesi manual yang masih aktif membuat jadwal yang tiba menjadi skipped_busy tanpa menghentikan sesi manual |
| AC-15 | Putus jaringan memicu retry; pemulihan tidak melewati waktu akhir jadwal dan tidak menghidupkan sesi yang sudah dihentikan operator |
| AC-16 | Restart aplikasi/server menghormati resume_after_restart, tidak menggandakan proses, dan tidak menjalankan jadwal yang sudah berakhir |
| AC-17 | Timer sesi, timer koneksi, dan posisi video mempunyai makna berbeda yang benar setelah reconnect |
| AC-18 | Tanpa API YouTube, panel menampilkan status publisher dan status tayang belum diverifikasi; thumbnail tidak diberi label live video |
| AC-19 | Log realtime dapat difilter dan diekspor tanpa stream key; Clear Log tidak menghapus riwayat server |
| AC-20 | Penghapusan media yang sedang dipakai atau direferensikan ditolak dengan daftar dependensi |
| AC-21 | Panel berfungsi pada viewport 360 px dan kontrol utama dapat digunakan tanpa hanya mengandalkan drag-and-drop |
| AC-22 | Uji 24 jam pada perangkat sasaran menyertakan pergantian media, pengulangan, satu simulasi putus jaringan, dan pemeriksaan tidak adanya OOM/crash |
| AC-23 | Stop ketika RECONNECTING membatalkan retry; batas waktu penghentian berlaku pada proses milik sesi |
| AC-24 | Mode random dengan loop mati melakukan tepat N pemilihan lalu selesai; random tanpa pengulangan tidak langsung mengulang video pada batas putaran bila N > 1 |

## 16. Tahapan pengerjaan

1. **Validasi engine:** buktikan publisher mode copy, pemeriksaan kompatibilitas, pergantian file, loop, pengacakan putaran, timestamp, dan terminasi proses pada perangkat sasaran. Hasil tahap ini menentukan implementasi pipeline media.
2. **Fondasi aplikasi:** autentikasi, konfigurasi, database, service, upload bertahap, FFprobe, thumbnail, dan library.
3. **Playlist dan kontrol:** revisi playlist, mode pemutaran, state sesi, start/stop/restart, log, dan event realtime.
4. **Otomatisasi server:** scheduler persisten, penanganan konflik, checkpoint, reconnect, serta pemulihan setelah reboot.
5. **Penyempurnaan antarmuka:** dashboard sesuai referensi, preview, monitoring, responsive layout, keadaan error/kosong, dan akses keyboard.
6. **Penerimaan rilis:** seluruh AC P0, pengujian 24 jam, panduan instalasi, setup YouTube Studio, backup dan pemulihan, serta contoh file media yang lolos validasi.

MVP dianggap selesai ketika alur dari upload sampai siaran manual/terjadwal berjalan melalui antarmuka, worker tetap mandiri dari browser, dan acceptance criteria lulus pada lingkungan yang didokumentasikan. Screenshot saja tidak cukup sebagai bukti aplikasi selesai.

## 17. Keputusan awal yang dapat diubah saat pengembangan

- Nama kerja: YouTube Live Server.
- Instalasi awal: server Linux mandiri, ARM64 atau x86-64, dioperasikan lewat browser.
- Pengguna: satu administrator; kapasitas awal satu publisher aktif.
- Input: video lokal hasil upload, tanpa integrasi Drive dalam MVP.
- Output: RTMPS sebagai pilihan awal, menggunakan stream key milik operator.
- Pemrosesan: media dipersiapkan terlebih dahulu; mode copy sebagai jalur P0.
- Preview: YouTube melalui URL/video ID opsional, dengan fallback thumbnail.
- Jadwal: sekali jalan dengan awal dan akhir eksplisit; zona waktu dipilih saat setup.
- Pemulihan: melanjutkan sesi yang terputus bila diaktifkan, dengan file aktif dimulai ulang dari awal.

## 18. Referensi teknis

Dokumentasi berikut digunakan untuk memeriksa batas teknis. Semua keputusan fitur lainnya adalah usulan produk yang diturunkan dari gambar. Dokumentasi diperiksa pada 5 Oktober 2026; validasi kembali parameter penyedia saat implementasi.

- **[S1]** [FFmpeg — stream copy dan opsi proses](https://www.ffmpeg.org/ffmpeg.html)
- **[S2]** [FFmpeg — concat demuxer](https://www.ffmpeg.org/ffmpeg-all.html#concat)
- **[S3]** [YouTube — encoder settings, bitrate dan resolusi](https://support.google.com/youtube/answer/2853702?hl=en)
- **[S4]** [YouTube Live Streaming API — liveStreams dan kesehatan ingest](https://developers.google.com/youtube/v3/live/docs/liveStreams)
- **[S5]** [YouTube Live Streaming API — liveBroadcasts dan lifecycle](https://developers.google.com/youtube/v3/live/docs/liveBroadcasts)
- **[S6]** [YouTube — menyiapkan siaran dengan encoder](https://support.google.com/youtube/answer/2907883?hl=en)
