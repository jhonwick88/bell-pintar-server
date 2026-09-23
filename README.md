# 🔔 Panduan Penggunaan Sistem Bell Pintar
> **Sistem Otomatisasi Bel Sekolah Cerdas & Studio Penyiaran Suara Modern**  
> *Solusi Penjadwalan Bel Otomatis, Text-to-Speech (TTS) AI, Input Suara (Dikte), dan Kendali Remote Berbasis Jaringan Lokal (LAN).*

---

## 📋 Daftar Isi
1. [Tentang Sistem](#-tentang-sistem)
2. [Kebutuhan Sistem & Instalasi](#-kebutuhan-sistem--instalasi)
3. [Akun Pengguna & Hak Akses (Login PIN)](#-akun-pengguna--hak-akses-login-pin)
4. [Panduan Fitur Utama](#-panduan-fitur-utama)
   - [4.1 Dashboard & Kontrol Cepat](#41-dashboard--kontrol-cepat)
   - [4.2 Manajemen Jadwal & Custom Preset (5 Hari / 6 Hari Sekolah)](#42-manajemen-jadwal--custom-preset-5-hari--6-hari-sekolah)
   - [4.3 Bank Suara (138+ Audio Bawaan & Upload Custom MP3)](#43-bank-suara-138-audio-bawaan--upload-custom-mp3)
   - [4.4 Studio Pengumuman TTS AI, Dikte Suara & CRUD Template](#44-studio-pengumuman-tts-ai-dikte-suara--crud-template)
   - [4.5 Kendali Remote HP Guru Piket & Scan QR Pairing](#45-kendali-remote-hp-guru-piket--scan-qr-pairing)
   - [4.6 Pengaturan Sistem, Identitas Sekolah & Lisensi](#46-pengaturan-sistem-identitas-sekolah--lisensi)
5. [Panduan Khusus Pembuatan Suara (Windows 7, 8, 10, dan 11)](#-panduan-khusus-pembuatan-suara-windows-7-8-10-dan-11)
6. [Layanan Kustom Fitur & Pengembangan Software (Pintar Labs)](#-layanan-kustom-fitur--pengembangan-software-pintar-labs)
7. [Tanya Jawab & Pemecahan Masalah (FAQ)](#-tanya-jawab--pemecahan-masalah-faq)

---

## 📖 Tentang Sistem

**Bell Pintar** dirancang untuk menggantikan bel lonceng konvensional dengan sistem otomatisasi berbasis komputer yang terintegrasi langsung ke pengeras suara (amplifier/TOA) sekolah.

### Keunggulan Utama:
- **Jadwal Otomatis Presisi:** Bel berbunyi tepat waktu sesuai detik jam sekolah tanpa keterlambatan.
- **Dukungan Banyak Preset:** Mendukung *Jadwal Reguler*, *Jadwal Khusus Hari Jumat*, *Jadwal Pekan Ujian*, *Bulan Ramadhan*, serta pembuatan **Custom Preset Baru** dengan pola **5 Hari** (Senin–Jumat) atau **6 Hari** (Senin–Sabtu).
- **138+ Koleksi Audio Bawaan:** Pustaka nada bel lengkap (jam pelajaran ke-1 s/d 12, istirahat, upacara, apel, kepulangan, 3 bahasa: Indonesia, Inggris, Arab, musik instrumen pengantar, himbauan, sirine, dll).
- **Auto Switch Amplifier (Relay):** Mengaktifkan amplifier otomatis beberapa detik sebelum bel berbunyi dan mematikannya kembali setelah suara selesai untuk menghemat listrik dan mencegah *noise/dengung*.
- **Studio Pengumuman TTS & Dikte Suara:** Siaran langsung teks-ke-suara alami (Bahasa Indonesia & Inggris) dilengkapi fitur **Dikte Suara (Speech-to-Text)** tanpa perlu mengetik, serta template pengumuman instan dengan nada chime pembuka.
- **Kendali Jarak Jauh (Remote Mobile):** Guru piket dapat membunyikan bel atau mengirim pengumuman langsung dari HP Android melalui jaringan Wi-Fi sekolah via Scan QR Code instan.
- **Audit Log & Keamanan:** Riwayat pembunyian bel tercatat lengkap (jam, pemicu, operator, status relay) serta proteksi kuota perangkat aktif.

---

## 💻 Kebutuhan Sistem & Instalasi

### 1. Perangkat Keras (Hardware)
- Komputer / Laptop Server Sekolah (Windows 7 SP1, Windows 8, Windows 10, atau Windows 11).
- Kabel Audio AUX 3.5mm dari port audio komputer ke input Amplifier / TOA Sekolah.
- *(Opsional)* Modul USB Relay Controller (untuk otomatisasi saklar daya amplifier).

### 2. Cara Menjalankan Server
1. Ekstrak paket aplikasi `bell_pintar` ke komputer server (misal di `D:\BellPintar` atau `C:\BellPintar`).
2. Jalankan file **`bell_server.exe`**.
3. Saat dijalankan, server akan secara otomatis:
   - Membuat direktori penyimpanan lokal `data/` (`data/bell.db`, `data/audio_custom/`, `data/tts_cache/`).
   - Menyinkronkan seluruh file `.mp3` dari `server/assets/audio/` langsung ke database sistem.
   - Menginisialisasi seluruh tabel database dan data bawaan (pengguna, jadwal reguler, nada suara, template pengumuman).
   - Membuka port layanan lokal di `http://0.0.0.0:8088`.
4. Buka aplikasi antarmuka pengguna **Bell Pintar** (Desktop Windows atau Android).

---

## 🔑 Akun Pengguna & Hak Akses (Login PIN)

Aplikasi menggunakan autentikasi berbasis PIN cepat untuk memudahkan guru dan staf sekolah:

| Pengguna | PIN Bawaan | Hak Akses & Peran |
| :--- | :---: | :--- |
| **Admin TU** | `741147` | **Akses Penuh:** Mengatur seluruh jadwal, preset baru, mengunggah audio custom, aktivasi lisensi, menyambungkan HP baru, dan konfigurasi relay. |
| **Guru Piket** | `432234` | **Akses Operasional:** Membunyikan bel darurat manual, mengganti preset jadwal aktif, dan menyiarkan pengumuman suara / TTS. |

---

## 🎛️ Panduan Fitur Utama

### 4.1 Dashboard & Kontrol Cepat
- **Hitung Mundur Bel Berikutnya:** Menampilkan jam pemicu, judul agenda bel berikutnya, dan hitung mundur (*countdown* detik).
- **Preset Aktif:** Menampilkan mode jadwal yang sedang berjalan. Anda dapat beralih preset sewaktu-waktu (misal saat hari hujan atau jam pulang dipercepat).
- **Tombol Bel Darurat / Manual:** Memungkinkan pembunyian bel instan 1-klik untuk panggilan darurat atau penanda manual.

### 4.2 Manajemen Jadwal & Custom Preset (5 Hari / 6 Hari Sekolah)
Masuk ke menu **Jadwal**:
1. **Pemilihan & Pembuatan Preset:**
   - Pilih preset yang ingin diedit (*Reguler, Jumat, Ujian, dll*).
   - Klik **"+ Buat Preset Baru"** untuk membuat preset kustom:
     - Pilih pola: **5 Hari Sekolah** (Sabtu & Minggu otomatis libur) atau **6 Hari Sekolah** (Hanya Minggu yang libur).
     - Pilih opsi duplikasi jadwal dari preset yang sudah ada agar tidak perlu mengetik ulang dari awal.
2. **Tab Hari & Fitur Kosongkan:**
   - Tab hari Senin s/d Minggu.
   - Pada hari libur atau saat ingin membersihkan jadwal hari tertentu, tekan tombol **"Kosongkan Hari Ini"** untuk menghapus seluruh jadwal di hari tersebut secara instan.
3. **Tambah & Edit Jam Bel:**
   - Klik **"+ Tambah Jam Baru"**:
     - Atur waktu dengan *TimePicker* visual.
     - Masukkan judul agenda (*"Masuk Jam Ke-1"*, *"Istirahat"*, dll).
     - Pilih nada bel dari pustaka audio (tersedia tombol uji suara).
     - Klik **"Simpan Jadwal"**.
   - Pada perangkat mobile/HP, geser item jadwal ke kanan (*swipe*) untuk menampilkan tombol **Edit** dan **Hapus**.

### 4.3 Bank Suara (138+ Audio Bawaan & Upload Custom MP3)
Masuk ke menu **Bank Suara**:
- **Koleksi Lengkap:** Tersedia 138+ nada bel siap pakai yang mencakup berbagai agenda sekolah, peringatan, dan instruksi dalam 3 bahasa.
- **Auto-Sync:** File audio baru yang ditambahkan ke folder `server/assets/audio/` akan otomatis dideteksi dan didaftarkan ke database saat server dinyalakan.
- **Unggah Suara Kustom:**
  1. Klik tombol **"+ Upload Audio"**.
  2. Masukkan judul dan kategori audio.
  3. Pilih file `.mp3` atau `.wav` dari komputer.
  4. Klik **"Upload & Simpan"**.
- **Proteksi Hapus:** Sistem memblokir penghapusan audio jika file tersebut sedang digunakan oleh jadwal aktif.

### 4.4 Studio Pengumuman TTS AI, Dikte Suara & CRUD Template
Masuk ke menu **Studio Pengumuman (TTS)**:
- **Siaran Langsung Text-to-Speech (TTS):** Ketik pesan pengumuman, lalu klik **"Siarkan Pengumuman Sekarang"**.
- **Fitur Dikte Suara (Speech-to-Text):**
  - Tekan tombol **"Dikte Lewat Suara"**.
  - Ucapkan pengumuman Anda; sistem secara otomatis mentranskripsikan ucapan menjadi teks pada form input tanpa perlu mengetik manual.
  - Dilengkapi dialog edukasi & konfirmasi izin mikrofon serta Bluetooth audio demi transparansi privasi pengguna.
- **Pengaturan Tempo Suara:** Geser slider tempo (0.6x santai s/d 1.5x cepat) dengan suara alami *Microsoft Andika*.
- **CRUD Template Pengumuman Cepat:**
  - **Tambah Template Baru:** Klik tombol **"+ Tambah Template"**.
  - Isi judul, naskah pengumuman, bahasa (*id-ID* atau *en-US*), dan pilih **Nada Pembuka / Chime** (dari 138+ nada audio dengan tombol tes nada).
  - **Edit & Hapus Template:** Setiap kartu template dilengkapi tombol edit (ikon pensil) dan tombol hapus (ikon sampah dengan dialog konfirmasi).
  - **1-Klik Siar:** Tekan tombol **"Siarkan Sekarang"** pada template untuk langsung memutar nada pembuka diikuti naskah pengumuman secara otomatis.

### 4.5 Kendali Remote HP Guru Piket & Scan QR Pairing
Guru piket dapat mengoperasikan bel sekolah dari HP Android di ruang piket atau lapangan:
1. Pastikan HP terhubung ke **Wi-Fi sekolah** yang sama dengan komputer server.
2. Di komputer server, buka **Pengaturan > Scan QR Pairing HP Guru Piket**.
3. Arahkan kamera HP / Google Lens ke QR Code tersebut.
4. Tautan *deep link* otomatis membuka aplikasi Bell Pintar di HP dan langsung menyambung ke IP server tanpa mengetik manual.
5. Masukkan PIN Guru Piket (`432234`) untuk mulai mengendalikan bel dan menyiarkan pengumuman.
6. **Manajemen Sesi:** Admin TU dapat melihat daftar HP yang sedang aktif dan mencabut izin akses kapan saja melalui dialog *Daftar Perangkat Aktif*. Sistem secara otomatis membebaskan kuota lisensi saat perangkat logout resmi.

### 4.6 Pengaturan Sistem, Identitas Sekolah & Lisensi
- **Identitas Sekolah (Nama Sekolah / Instansi):**
  - Digunakan sebagai bukti kepemilikan sah saat aktivasi lisensi produk.
  - Menjadi identitas nama server saat guru piket menghubungkan HP Android via Wi-Fi.
  - Tampil pada kop aplikasi dan laporan audit log sekolah.
- **Aktivasi & Perpanjangan Lisensi:** Masukkan kode lisensi resmi dari Pintar Labs untuk membuka fitur Pro (durasi lisensi, custom audio, dan kuota remote mobile).
- **Pengaturan Relay:** Mengatur jeda otomatis saklar daya amplifier sebelum dan sesudah bel berbunyi.
- **Audit Log Bel:** Membuka riwayat 50 pemicuan bel terakhir secara detail.

---

## 🎙️ Panduan Khusus Pembuatan Suara (Windows 7, 8, 10, dan 11)

- **Windows 10 & Windows 11:** Memiliki dukungan suara Bahasa Indonesia alami (*Microsoft Andika*). TTS otomatis terdengar merdu dan jernih.
- **Windows 7 & Windows 8:** Sistem operasi lawas tidak memiliki suara Bahasa Indonesia bawaan. Disarankan menggunakan file `.mp3` dari menu **Bank Suara**.
- **Tips Suara AI Tambahan:** Anda dapat membuat suara ramah dan profesional secara gratis melalui situs generator seperti **[TTSMaker.com](https://ttsmaker.com)** atau **[VoiceMaker.in](https://voicemaker.in)**, lalu mengunggahnya ke menu Bank Suara.

---

## 💼 Layanan Kustom Fitur & Pengembangan Software (Pintar Labs)

Sistem Bell Pintar dikembangkan secara profesional oleh **Pintar Labs**. Kami melayani:
- **Kustomisasi Fitur Bell Pintar:** Integrasi modul IoT relay jarak jauh, RFID absensi bel otomatis, smart display TV LED sekolah, integrasi running text, dsb.
- **Pembuatan Software & Aplikasi:**
  - Sistem Informasi Akademik Sekolah (SIAKAD) & Absensi Digital Siswa/Guru.
  - Aplikasi Kasir / Point of Sale (POS) Koperasi Sekolah & Bisnis.
  - Rancang bangun aplikasi **Web**, **Mobile Android & iOS**, serta **Desktop Windows**.

### Kontak Pengembang:
- 📱 **WhatsApp:** [0821-3293-5169](https://wa.me/6282132935169?text=Halo%20Developer%20Bell%20Pintar,%20saya%20tertarik%20untuk%20konsultasi%20custom%20fitur%20/%20pembuatan%20aplikasi.)
- ✉️ **Email:** [labspintar@gmail.com](mailto:labspintar@gmail.com)

---

## ❓ Tanya Jawab & Pemecahan Masalah (FAQ)

### Q1: Bel tidak berbunyi pada jam yang sudah ditentukan?
- Pastikan jendela server `bell_server.exe` tetap berjalan di komputer.
- Periksa kabel audio dari komputer ke amplifier terpasang kencang di port warna hijau (*Line Out*).
- Pastikan preset jadwal yang aktif sesuai dengan jadwal yang ingin dibunyikan.

### Q2: HP Android tidak bisa terhubung ke server bel?
- Pastikan HP dan komputer server terhubung ke router Wi-Fi yang sama (satu segmen IP lokal).
- Pastikan Windows Firewall di komputer server mengizinkan akses ke aplikasi `bell_server.exe` atau port `8088`.

### Q3: Apakah data jadwal akan hilang jika komputer mati lampu?
- **Tidak.** Seluruh data jadwal, preset, template pengumuman, rekaman audio, dan lisensi tersimpan aman di database SQLite lokal (`data/bell.db`).

---
*© Bell Pintar by Pintar Labs — Solusi Digital Sekolah Pintar Indonesia.*
