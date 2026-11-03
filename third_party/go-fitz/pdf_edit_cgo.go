//go:build cgo && !nocgo

package fitz

// Implementation of the pdf_edit.go API against MuPDF's pdf_* layer.
//
// Every shim wraps its MuPDF call in fz_try/fz_catch — cgo must never let
// an fz_throw cross into Go — and takes (char *err, int errlen) to return
// the exception message. Shims return 0 on success, -1 on a MuPDF error
// (message in err), -2 when the fz_document is not a PDF, -3 when a
// referenced annotation/field vanished.
//
// All objects the shims load (pdf_page, graft maps, scratch pdf_obj) are
// dropped on every exit path. Strings handed to Go are fz_strdup'd copies
// freed with the drop_string shim from fitz_cgo.go. Inner comments use
// // style: block comments would close the preamble (C comments do not
// nest).

/*
#include <mupdf/pdf.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

static void gopdf_err(fz_context *ctx, char *err, int errlen) {
	if (err && errlen > 0)
		fz_strlcpy(err, fz_caught_message(ctx), (size_t)errlen);
}

// downcast fz_document to its pdf_document; NULL when not a PDF.
pdf_document *gopdf_as_pdf(fz_context *ctx, fz_document *doc) {
	return pdf_document_from_fz_document(ctx, doc);
}

// fz_strdup'd string free — defined in fitz_cgo.go's preamble.
void drop_string(fz_context *ctx, char *s);

// load one pdf_page; NULL + err on failure.
pdf_page *gopdf_load_page(fz_context *ctx, pdf_document *pdf, int n, char *err, int errlen) {
	pdf_page *p = NULL;
	fz_try(ctx) {
		p = pdf_load_page(ctx, pdf, n);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return NULL;
	}
	return p;
}

static void gopdf_fill_write_opts(pdf_write_options *opts,
	int incremental, int garbage, int appearance, int sanitize, int pretty, int ascii,
	int encrypt, int permissions, const char *opwd, const char *upwd) {
	memset(opts, 0, sizeof(*opts));
	opts->do_incremental = incremental;
	opts->do_garbage = garbage;
	opts->do_appearance = appearance;
	opts->do_sanitize = sanitize;
	opts->do_pretty = pretty;
	opts->do_ascii = ascii;
	opts->do_encrypt = encrypt;
	opts->permissions = permissions;
	if (opwd)
		fz_strlcpy(opts->opwd_utf8, opwd, sizeof(opts->opwd_utf8));
	if (upwd)
		fz_strlcpy(opts->upwd_utf8, upwd, sizeof(opts->upwd_utf8));
}

// Save doc to filename.
int gopdf_save_pdf(fz_context *ctx, fz_document *doc, const char *filename,
	int incremental, int garbage, int appearance, int sanitize, int pretty, int ascii,
	int encrypt, int permissions, const char *opwd, const char *upwd,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_write_options opts;
	gopdf_fill_write_opts(&opts, incremental, garbage, appearance, sanitize, pretty, ascii,
		encrypt, permissions, opwd, upwd);
	fz_try(ctx) {
		pdf_save_document(ctx, pdf, filename, &opts);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// Graft the listed pages of doc into a fresh document and save it.
// Mirrors mutool merge: graft each page object through the map, then insert
// it explicitly (pdf_graft_mapped_page mishandles page_from).
int gopdf_extract_pages(fz_context *ctx, fz_document *doc, const int *pages, int npages, const char *filename,
	int incremental, int garbage, int appearance, int sanitize, int pretty, int ascii,
	int encrypt, int permissions, const char *opwd, const char *upwd,
	char *err, int errlen) {
	pdf_document *src = pdf_document_from_fz_document(ctx, doc);
	if (!src) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_document *out = NULL;
	pdf_graft_map *map = NULL;
	fz_try(ctx) {
		int nsrc = pdf_count_pages(ctx, src);
		for (int i = 0; i < npages; i++)
			if (pages[i] < 0 || pages[i] >= nsrc)
				fz_throw(ctx, FZ_ERROR_ARGUMENT, "page %d out of range", pages[i]);
		out = pdf_create_document(ctx);
		map = pdf_new_graft_map(ctx, out);
		for (int i = 0; i < npages; i++) {
			pdf_obj *page = pdf_lookup_page_obj(ctx, src, pages[i]);
			pdf_obj *g = pdf_graft_mapped_object(ctx, map, page);
			pdf_insert_page(ctx, out, i, g);
			pdf_drop_obj(ctx, g);
		}
		pdf_write_options opts;
		gopdf_fill_write_opts(&opts, incremental, garbage, appearance, sanitize, pretty, ascii,
			encrypt, permissions, opwd, upwd);
		pdf_save_document(ctx, out, filename, &opts);
	}
	fz_always(ctx) {
		if (map) pdf_drop_graft_map(ctx, map);
		if (out) pdf_drop_document(ctx, out);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// Open src_path in the SAME context and graft its listed pages at `at`.
// Cross-document grafting must share one context (MuPDF object caches are
// per-context); opening the source inside the destination's context gives
// exactly that. Mirrors mutool merge: graft each page object through the
// map, then insert it explicitly.
int gopdf_append_pages(fz_context *ctx, fz_document *dst_doc, const char *src_path,
	const int *pages, int npages, int at,
	char *err, int errlen) {
	pdf_document *dst = pdf_document_from_fz_document(ctx, dst_doc);
	if (!dst) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_document *src = NULL;
	pdf_graft_map *map = NULL;
	fz_try(ctx) {
		src = fz_open_document(ctx, src_path);
		pdf_document *psrc = pdf_document_from_fz_document(ctx, src);
		if (!psrc)
			fz_throw(ctx, FZ_ERROR_ARGUMENT, "%s", "source document is not a PDF");
		int nsrc = fz_count_pages(ctx, src);
		for (int i = 0; i < npages; i++)
			if (pages[i] < 0 || pages[i] >= nsrc)
				fz_throw(ctx, FZ_ERROR_ARGUMENT, "source page %d out of range", pages[i]);
		map = pdf_new_graft_map(ctx, dst);
		for (int i = 0; i < npages; i++) {
			pdf_obj *page = pdf_lookup_page_obj(ctx, psrc, pages[i]);
			pdf_obj *g = pdf_graft_mapped_object(ctx, map, page);
			pdf_insert_page(ctx, dst, at + i, g);
			pdf_drop_obj(ctx, g);
		}
	}
	fz_always(ctx) {
		if (map) pdf_drop_graft_map(ctx, map);
		if (src) fz_drop_document(ctx, src);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// ---- page organization ----

int gopdf_insert_blank_page(fz_context *ctx, fz_document *doc, int at, float w, float h,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_obj *page = pdf_new_dict(ctx, pdf, 4);
		pdf_dict_put(ctx, page, PDF_NAME(Type), PDF_NAME(Page));
		pdf_dict_put(ctx, page, PDF_NAME(Resources), pdf_new_dict(ctx, pdf, 0));
		fz_rect mb = { 0, 0, w, h };
		pdf_dict_put_rect(ctx, page, PDF_NAME(MediaBox), mb);
		pdf_obj *ref = pdf_add_object(ctx, pdf, page);
		pdf_insert_page(ctx, pdf, at, ref);
		pdf_drop_obj(ctx, ref);
		pdf_drop_obj(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

int gopdf_delete_page(fz_context *ctx, fz_document *doc, int n, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_delete_page(ctx, pdf, n);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

int gopdf_delete_page_range(fz_context *ctx, fz_document *doc, int start, int end, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_delete_page_range(ctx, pdf, start, end);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// rot is absolute: 0, 90, 180 or 270 (validated in Go).
int gopdf_set_page_rotation(fz_context *ctx, fz_document *doc, int n, int rot, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_obj *po = pdf_lookup_page_obj(ctx, pdf, n);
		if (!po)
			fz_throw(ctx, FZ_ERROR_ARGUMENT, "page %d not found", n);
		pdf_dict_put_int(ctx, po, PDF_NAME(Rotate), rot);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// ---- authentication ----

int gopdf_authenticate(fz_context *ctx, fz_document *doc, const char *pw, char *err, int errlen) {
	int r = 0;
	fz_try(ctx) {
		r = fz_authenticate_password(ctx, doc, pw);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return r;
}

int gopdf_has_permission(fz_context *ctx, fz_document *doc, int perm, char *err, int errlen) {
	int r = 0;
	fz_try(ctx) {
		r = fz_has_permission(ctx, doc, (fz_permission)perm);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return r;
}

// ---- annotations ----

// Advance to the next annotation of *cur (NULL starts). Fills every field;
// strings are fz_strdup'd copies the caller frees with drop_string.
// Returns 1 = filled, 0 = no more, <0 = error.
int gopdf_annot_step(fz_context *ctx, pdf_page *page, pdf_annot **cur,
	int64_t *num, int *atype, float *rect4, char **contents, char **author,
	int *color_n, float *color4, float *opacity, int *quads_n,
	char *err, int errlen) {
	pdf_annot *a = NULL;
	fz_try(ctx) {
		a = *cur ? pdf_next_annot(ctx, *cur) : pdf_first_annot(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	if (!a) {
		*cur = NULL;
		return 0;
	}
	*cur = a;
	fz_try(ctx) {
		*num = pdf_to_num(ctx, pdf_annot_obj(ctx, a));
		*atype = (int)pdf_annot_type(ctx, a);
		// pdf_annot_rect throws for markup annots (highlight & co have no
		// /Rect of their own) — fall back to the annotation's real bbox.
		fz_rect r;
		fz_try(ctx) {
			r = pdf_annot_rect(ctx, a);
		}
		fz_catch(ctx) {
			r = pdf_bound_annot(ctx, a);
		}
		rect4[0] = r.x0; rect4[1] = r.y0; rect4[2] = r.x1; rect4[3] = r.y1;
		const char *c = pdf_annot_contents(ctx, a);
		*contents = c ? fz_strdup(ctx, c) : NULL;
		const char *au = pdf_annot_author(ctx, a);
		*author = au ? fz_strdup(ctx, au) : NULL;
		int n = 0;
		pdf_annot_color(ctx, a, &n, color4);
		*color_n = n;
		// /CA opacity; 1 when the entry is absent.
		pdf_obj *obj = pdf_annot_obj(ctx, a);
		*opacity = pdf_dict_get(ctx, obj, PDF_NAME(CA))
			? pdf_dict_get_real(ctx, obj, PDF_NAME(CA)) : 1.0f;
		// Quad point geometry: read /QuadPoints straight off the annot
		// object (8 reals per quad, user space) — the
		// pdf_annot_quad_point_count cache is only populated for annots
		// edited this session, not for freshly loaded ones.
		pdf_obj *qp = pdf_dict_get(ctx, obj, PDF_NAME(QuadPoints));
		*quads_n = qp ? (int)(pdf_array_len(ctx, qp) / 8) : 0;
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 1;
}

// Fill out with the annot's /QuadPoints, transformed from PDF user space
// to fitz page space (top-left origin) via the page transform. Returns the
// quad count; <0 on error.
int gopdf_annot_quads(fz_context *ctx, pdf_page *page, pdf_annot *a, fz_quad *out, int max, char *err, int errlen) {
	int n = 0;
	fz_try(ctx) {
		pdf_obj *qp = pdf_dict_get(ctx, pdf_annot_obj(ctx, a), PDF_NAME(QuadPoints));
		if (!qp) return 0;
		n = (int)(pdf_array_len(ctx, qp) / 8);
		if (n > max) n = max;
		fz_matrix ctm;
		pdf_page_transform(ctx, page, NULL, &ctm);
		for (int i = 0; i < n; i++) {
			fz_point ul = { pdf_array_get_real(ctx, qp, i*8+0), pdf_array_get_real(ctx, qp, i*8+1) };
			fz_point ur = { pdf_array_get_real(ctx, qp, i*8+2), pdf_array_get_real(ctx, qp, i*8+3) };
			fz_point ll = { pdf_array_get_real(ctx, qp, i*8+4), pdf_array_get_real(ctx, qp, i*8+5) };
			fz_point lr = { pdf_array_get_real(ctx, qp, i*8+6), pdf_array_get_real(ctx, qp, i*8+7) };
			out[i].ul = fz_transform_point(ul, ctm);
			out[i].ur = fz_transform_point(ur, ctm);
			out[i].ll = fz_transform_point(ll, ctm);
			out[i].lr = fz_transform_point(lr, ctm);
		}
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return n;
}

static pdf_annot *gopdf_find_annot(fz_context *ctx, pdf_page *page, int64_t num) {
	for (pdf_annot *a = pdf_first_annot(ctx, page); a; a = pdf_next_annot(ctx, a))
		if (pdf_to_num(ctx, pdf_annot_obj(ctx, a)) == num)
			return a;
	return NULL;
}

// Create an annotation of atype at rect4, regenerate the page's
// appearances and report the new annotation's object number. rect4 is
// applied only for rect-based annots (notes, squares, ink, ...): markup
// annots (highlight, underline, squiggly, strikeout) derive their rect
// from quad points and reject pdf_set_annot_rect, so their callers set
// QuadPoints through AnnotEdit instead.
int gopdf_annot_create(fz_context *ctx, fz_document *doc, int page_no, int atype, const float *rect4,
	int with_rect, int64_t *out_num, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *a = pdf_create_annot(ctx, page, (enum pdf_annot_type)atype);
		if (with_rect) {
			fz_rect r = { rect4[0], rect4[1], rect4[2], rect4[3] };
			pdf_set_annot_rect(ctx, a, r);
		}
		*out_num = pdf_to_num(ctx, pdf_annot_obj(ctx, a));
		pdf_update_page(ctx, page);
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// Apply a partial edit to annotation num on page_no, then regenerate
// appearances. Each set_ flag precedes its value.
int gopdf_annot_edit(fz_context *ctx, fz_document *doc, int page_no, int64_t num,
	int set_rect, const float *rect4,
	int set_contents, const char *contents,
	int set_author, const char *author,
	int set_color, const float *color, int color_n,
	int set_opacity, float opacity,
	int set_border, float border,
	int set_quads, const fz_quad *quads, int nquads,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	int rc = 0;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *a = gopdf_find_annot(ctx, page, num);
		if (!a) {
			rc = -3;
		} else {
			if (set_rect) {
				fz_rect r = { rect4[0], rect4[1], rect4[2], rect4[3] };
				pdf_set_annot_rect(ctx, a, r);
			}
			if (set_contents)
				pdf_set_annot_contents(ctx, a, contents);
			if (set_author)
				pdf_set_annot_author(ctx, a, author);
			if (set_color)
				pdf_set_annot_color(ctx, a, color_n, color);
			if (set_opacity)
				pdf_set_annot_opacity(ctx, a, opacity);
			if (set_border)
				pdf_set_annot_border_width(ctx, a, border);
			if (set_quads)
				pdf_set_annot_quad_points(ctx, a, nquads, quads);
			pdf_update_page(ctx, page);
		}
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return rc;
}

int gopdf_annot_delete(fz_context *ctx, fz_document *doc, int page_no, int64_t num, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	int rc = 0;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *a = gopdf_find_annot(ctx, page, num);
		if (!a) {
			rc = -3;
		} else {
			pdf_delete_annot(ctx, page, a);
			pdf_update_page(ctx, page);
		}
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return rc;
}

int gopdf_annots_refresh(fz_context *ctx, fz_document *doc, int page_no, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_update_page(ctx, page);
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// ---- form fields ----

// Same stepping protocol as gopdf_annot_step, over widgets.
int gopdf_widget_step(fz_context *ctx, pdf_page *page, pdf_annot **cur,
	int64_t *num, int *wtype, float *rect4, char **label, char **value,
	char *err, int errlen) {
	pdf_annot *w = NULL;
	fz_try(ctx) {
		w = *cur ? pdf_next_widget(ctx, *cur) : pdf_first_widget(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	if (!w) {
		*cur = NULL;
		return 0;
	}
	*cur = w;
	fz_try(ctx) {
		pdf_obj *field = pdf_annot_obj(ctx, w);
		*num = pdf_to_num(ctx, field);
		*wtype = (int)pdf_widget_type(ctx, w);
		fz_rect r = pdf_annot_rect(ctx, w);
		rect4[0] = r.x0; rect4[1] = r.y0; rect4[2] = r.x1; rect4[3] = r.y1;
		const char *l = pdf_field_label(ctx, field);
		*label = l ? fz_strdup(ctx, l) : NULL;
		const char *v = pdf_field_value(ctx, field);
		*value = v ? fz_strdup(ctx, v) : NULL;
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 1;
}

static pdf_annot *gopdf_find_widget(fz_context *ctx, pdf_page *page, int64_t num) {
	for (pdf_annot *w = pdf_first_widget(ctx, page); w; w = pdf_next_widget(ctx, w))
		if (pdf_to_num(ctx, pdf_annot_obj(ctx, w)) == num)
			return w;
	return NULL;
}

int gopdf_field_set_value(fz_context *ctx, fz_document *doc, int page_no, int64_t num,
	const char *value, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	int rc = 0;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *w = gopdf_find_widget(ctx, page, num);
		if (!w) {
			rc = -3;
		} else {
			pdf_obj *field = pdf_annot_obj(ctx, w);
			if (!pdf_set_field_value(ctx, pdf, field, value, 1))
				fz_throw(ctx, FZ_ERROR_ARGUMENT, "field rejects value %s", value ? value : "");
			pdf_update_page(ctx, page);
		}
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return rc;
}

int gopdf_field_reset(fz_context *ctx, fz_document *doc, int page_no, int64_t num, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	int rc = 0;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *w = gopdf_find_widget(ctx, page, num);
		if (!w) {
			rc = -3;
		} else {
			pdf_field_reset(ctx, pdf, pdf_annot_obj(ctx, w));
			pdf_update_page(ctx, page);
		}
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return rc;
}

// ---- metadata ----

int gopdf_set_metadata(fz_context *ctx, fz_document *doc, const char *key, const char *value,
	char *err, int errlen) {
	fz_try(ctx) {
		fz_set_metadata(ctx, doc, key, value);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// ---- redaction ----

int gopdf_redact_add(fz_context *ctx, fz_document *doc, int page_no, const float *rect4,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_annot *a = pdf_create_annot(ctx, page, PDF_ANNOT_REDACT);
		fz_rect r = { rect4[0], rect4[1], rect4[2], rect4[3] };
		pdf_set_annot_rect(ctx, a, r);
		pdf_update_page(ctx, page);
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// Apply every redact annotation on the page: pixels blacked, covered line
// art dropped, overlapping text removed (secure defaults).
int gopdf_redact_apply(fz_context *ctx, fz_document *doc, int page_no, int black_boxes,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	pdf_page *page = NULL;
	fz_try(ctx) {
		page = pdf_load_page(ctx, pdf, page_no);
		pdf_redact_options opts;
		memset(&opts, 0, sizeof(opts));
		opts.black_boxes = black_boxes;
		opts.image_method = PDF_REDACT_IMAGE_PIXELS;
		opts.line_art = PDF_REDACT_LINE_ART_REMOVE_IF_COVERED;
		opts.text = PDF_REDACT_TEXT_REMOVE;
		pdf_redact_page(ctx, pdf, page, &opts);
		pdf_update_page(ctx, page);
	}
	fz_always(ctx) {
		if (page) pdf_drop_page(ctx, page);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

// ---- optional content (layers) ----

int gopdf_layer_config_count(fz_context *ctx, fz_document *doc, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	int n = 0;
	fz_try(ctx) {
		n = pdf_count_layer_configs(ctx, pdf);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return n;
}

// Fill name/creator for config i; strings are fz_strdup'd copies.
int gopdf_layer_config_info(fz_context *ctx, fz_document *doc, int i, char **name, char **creator,
	char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_layer_config info;
		memset(&info, 0, sizeof(info));
		pdf_layer_config_info(ctx, pdf, i, &info);
		*name = info.name ? fz_strdup(ctx, info.name) : NULL;
		*creator = info.creator ? fz_strdup(ctx, info.creator) : NULL;
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}

int gopdf_layer_config_select(fz_context *ctx, fz_document *doc, int i, char *err, int errlen) {
	pdf_document *pdf = pdf_document_from_fz_document(ctx, doc);
	if (!pdf) {
		if (err) snprintf(err, (size_t)errlen, "%s", "document is not a PDF");
		return -2;
	}
	fz_try(ctx) {
		pdf_select_layer_config(ctx, pdf, i);
	}
	fz_catch(ctx) {
		gopdf_err(ctx, err, errlen);
		return -1;
	}
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

// errBuf is the C error-message sink every shim writes into on failure.
type errBuf [512]C.char

func (b *errBuf) ptr() *C.char { return (*C.char)(unsafe.Pointer(&b[0])) }
func (b *errBuf) len() C.int   { return C.int(len(b)) }

// opErr converts a shim return code into a Go error: 0 = nil, -2 =
// ErrNotPDF, -3 = ErrStaleRef, otherwise the MuPDF message.
func opErr(op string, b *errBuf, rc C.int) error {
	switch int(rc) {
	case 0:
		return nil
	case -2:
		return fmt.Errorf("fitz: %s: %w", op, ErrNotPDF)
	case -3:
		return fmt.Errorf("fitz: %s: %w", op, ErrStaleRef)
	default:
		msg := C.GoString(b.ptr())
		if msg == "" {
			msg = "mupdf error"
		}
		return fmt.Errorf("fitz: %s: %s", op, msg)
	}
}

func gBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intPtr(pages []int) *C.int {
	if len(pages) == 0 {
		return nil
	}
	// C ints are 4 bytes; Go ints are 8 — pass a converted copy, not the
	// Go slice's memory.
	c := make([]C.int, len(pages))
	for i, v := range pages {
		c[i] = C.int(v)
	}
	return &c[0]
}

// NeedsPassword reports whether the document must be authenticated before use.
func (f *Document) NeedsPassword() bool {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	return int(C.fz_needs_password(f.ctx, f.doc)) != 0
}

// Authenticate tries to decrypt the document with password. It returns the
// authentication bits (AuthUserPassword|AuthOwnerPassword; 0 on rejection).
func (f *Document) Authenticate(password string) (int, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	cpw := C.CString(password)
	defer C.free(unsafe.Pointer(cpw))
	rc := C.gopdf_authenticate(f.ctx, f.doc, cpw, b.ptr(), b.len())
	if rc < 0 {
		return 0, opErr("authenticate", &b, rc)
	}
	return int(rc), nil
}

// HasPermission reports whether the (already authenticated) document grants
// the permission.
func (f *Document) HasPermission(p Permission) bool {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_has_permission(f.ctx, f.doc, C.int(rune(p)), b.ptr(), b.len())
	return rc > 0
}

// Save writes the document. An incremental save goes straight to path —
// which must be the file the document was opened from — appending an update
// section so the original bytes stay byte-for-byte intact. A full save to
// the open file itself is written directly, like mutool clean, keeping the
// document's own view consistent; a full save to any other path is atomic
// (written to a sibling temp file, then renamed).
func (f *Document) Save(path string, opts SaveOptions) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if opts.Incremental {
		openPath := f.path
		if openPath == "" {
			return fmt.Errorf("fitz: save: incremental saves need a file-backed document")
		}
		if abs, aerr := filepath.Abs(path); aerr == nil {
			if absOpen, oerr := filepath.Abs(openPath); oerr == nil && abs != absOpen {
				return fmt.Errorf("fitz: save: incremental save must target the open file %s", openPath)
			}
		}
	}
	if opts.Incremental && opts.Encryption != EncryptKeep {
		return fmt.Errorf("fitz: save: incremental saves keep the existing encryption")
	}

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	copwd := C.CString(opts.OwnerPassword)
	defer C.free(unsafe.Pointer(copwd))
	cupwd := C.CString(opts.UserPassword)
	defer C.free(unsafe.Pointer(cupwd))

	if !opts.Incremental {
		target := path
		if abs, err := filepath.Abs(path); err == nil {
			target = abs
		}
		sameFile := false
		if openPath := f.path; openPath != "" {
			if absOpen, err := filepath.Abs(openPath); err == nil {
				sameFile = absOpen == target
			}
		}
		if !sameFile {
			// Atomic write: save to a sibling temp file, rename over the target.
			tmp, err := os.CreateTemp(filepath.Dir(target), ".gopdf-save-*.pdf")
			if err != nil {
				return fmt.Errorf("fitz: save: %w", err)
			}
			tmpName := tmp.Name()
			tmp.Close()
			os.Remove(tmpName) // MuPDF creates the file itself
			ctmp := C.CString(tmpName)
			defer C.free(unsafe.Pointer(ctmp))
			var b errBuf
			rc := C.gopdf_save_pdf(f.ctx, f.doc, ctmp,
				0, C.int(opts.Garbage), gBool(opts.Appearance), gBool(opts.Sanitize),
				gBool(opts.Pretty), gBool(opts.ASCII),
				C.int(opts.Encryption), C.int(opts.Permissions), copwd, cupwd,
				b.ptr(), b.len())
			if rc != 0 {
				os.Remove(tmpName)
				return opErr("save", &b, rc)
			}
			if err := os.Rename(tmpName, target); err != nil {
				os.Remove(tmpName)
				return fmt.Errorf("fitz: save: replace %s: %w", path, err)
			}
			return nil
		}
	}

	var b errBuf
	rc := C.gopdf_save_pdf(f.ctx, f.doc, cpath,
		gBool(opts.Incremental), C.int(opts.Garbage), gBool(opts.Appearance), gBool(opts.Sanitize),
		gBool(opts.Pretty), gBool(opts.ASCII),
		C.int(opts.Encryption), C.int(opts.Permissions), copwd, cupwd,
		b.ptr(), b.len())
	return opErr("save", &b, rc)
}

// InsertBlankPage inserts a blank page of w×h points at index at (0-based;
// NumPage() appends).
func (f *Document) InsertBlankPage(at int, w, h float64) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_insert_blank_page(f.ctx, f.doc, C.int(at), C.float(w), C.float(h), b.ptr(), b.len())
	return opErr("insert blank page", &b, rc)
}

// DeletePage removes the page at index n (0-based).
func (f *Document) DeletePage(n int) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_delete_page(f.ctx, f.doc, C.int(n), b.ptr(), b.len())
	return opErr("delete page", &b, rc)
}

// DeletePageRange removes pages start..end inclusive (0-based indices).
func (f *Document) DeletePageRange(start, end int) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_delete_page_range(f.ctx, f.doc, C.int(start), C.int(end), b.ptr(), b.len())
	return opErr("delete pages", &b, rc)
}

// SetPageRotation writes the page's absolute /Rotate value (0, 90, 180, 270).
func (f *Document) SetPageRotation(n, rot int) error {
	if rot != 0 && rot != 90 && rot != 180 && rot != 270 {
		return fmt.Errorf("fitz: page rotation must be 0, 90, 180 or 270, got %d", rot)
	}
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_set_page_rotation(f.ctx, f.doc, C.int(n), C.int(rot), b.ptr(), b.len())
	return opErr("rotate page", &b, rc)
}

// AppendPagesFromFile opens srcPath inside this document's context and
// grafts the listed 0-based source pages (nil = all) at index at, updating
// cross-document references. The graft map keeps duplicated shared
// resources shared across appends.
func (f *Document) AppendPagesFromFile(srcPath string, pages []int, at int) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	srcCount := 0
	{
		src, err := New(srcPath)
		if err != nil {
			return fmt.Errorf("fitz: append: open source: %w", err)
		}
		srcCount = src.NumPage()
		src.Close()
	}
	if pages == nil {
		pages = make([]int, srcCount)
		for i := range pages {
			pages[i] = i
		}
	}
	if len(pages) == 0 {
		return nil
	}

	var b errBuf
	csrc := C.CString(srcPath)
	defer C.free(unsafe.Pointer(csrc))
	rc := C.gopdf_append_pages(f.ctx, f.doc, csrc, intPtr(pages), C.int(len(pages)), C.int(at), b.ptr(), b.len())
	return opErr("append pages", &b, rc)
}

// ExtractPagesToFile copies the listed 0-based pages of this document into
// a new document and saves it to path. opts.Incremental is ignored.
func (f *Document) ExtractPagesToFile(pages []int, path string, opts SaveOptions) error {
	if len(pages) == 0 {
		return fmt.Errorf("fitz: extract: no pages selected")
	}
	f.mtx.Lock()
	defer f.mtx.Unlock()

	var b errBuf
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	copwd := C.CString(opts.OwnerPassword)
	defer C.free(unsafe.Pointer(copwd))
	cupwd := C.CString(opts.UserPassword)
	defer C.free(unsafe.Pointer(cupwd))
	rc := C.gopdf_extract_pages(f.ctx, f.doc, intPtr(pages), C.int(len(pages)), cpath,
		0, C.int(opts.Garbage), gBool(opts.Appearance), gBool(opts.Sanitize),
		gBool(opts.Pretty), gBool(opts.ASCII),
		C.int(opts.Encryption), C.int(opts.Permissions), copwd, cupwd,
		b.ptr(), b.len())
	return opErr("extract pages", &b, rc)
}

// Annots lists the annotations on one page. Widget annotations (form
// fields) are listed by Fields instead.
func (f *Document) Annots(pageNumber int) ([]Annot, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	pdf := C.gopdf_as_pdf(f.ctx, f.doc)
	if pdf == nil {
		return nil, fmt.Errorf("fitz: annots: %w", ErrNotPDF)
	}

	var b errBuf
	page := C.gopdf_load_page(f.ctx, pdf, C.int(pageNumber), b.ptr(), b.len())
	if page == nil {
		return nil, opErr("annots", &b, -1)
	}
	defer C.pdf_drop_page(f.ctx, page)

	var out []Annot
	var cur *C.struct_pdf_annot
	for {
		var (
			id       C.int64_t
			atype    C.int
			rect     [4]C.float
			color    [4]C.float
			colorN   C.int
			opacity  C.float
			quadsN   C.int
			contents *C.char
			author   *C.char
		)
		rc := C.gopdf_annot_step(f.ctx, page, &cur, &id, &atype, &rect[0], &contents, &author,
			&colorN, &color[0], &opacity, &quadsN, b.ptr(), b.len())
		if rc == 0 {
			break
		}
		if rc < 0 {
			return nil, opErr("annots", &b, rc)
		}
		a := Annot{ID: int64(id),
			Type:    AnnotType(atype),
			Rect:    Rect{float64(rect[0]), float64(rect[1]), float64(rect[2]), float64(rect[3])},
			Opacity: float64(opacity),
		}
		if contents != nil {
			a.Contents = C.GoString(contents)
			C.drop_string(f.ctx, contents)
		}
		if author != nil {
			a.Author = C.GoString(author)
			C.drop_string(f.ctx, author)
		}
		if colorN > 0 && colorN <= 4 {
			for i := 0; i < int(colorN); i++ {
				a.Color = append(a.Color, float64(color[i]))
			}
		}
		if quadsN > 0 {
			qs := make([]C.fz_quad, quadsN)
			n := C.gopdf_annot_quads(f.ctx, page, cur, &qs[0], quadsN, b.ptr(), b.len())
			if n < 0 {
				return nil, opErr("annots", &b, n)
			}
			for i := 0; i < int(n); i++ {
				q := qs[i]
				a.QuadPoints = append(a.QuadPoints, Quad{
					UL: Point{float64(q.ul.x), float64(q.ul.y)},
					UR: Point{float64(q.ur.x), float64(q.ur.y)},
					LL: Point{float64(q.ll.x), float64(q.ll.y)},
					LR: Point{float64(q.lr.x), float64(q.lr.y)},
				})
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// CreateAnnot adds an annotation of type t, applies e, regenerates
// appearances and returns the new annotation's ID. A failed property edit
// removes the half-configured annotation again. For rect-based annots
// (notes, squares, ink, stamps) e.Rect positions the annot; markup annots
// (highlight, underline, squiggly, strikeout) derive their rect from
// e.QuadPoints instead — setting Rect on them is rejected by MuPDF.
func (f *Document) CreateAnnot(pageNumber int, t AnnotType, rect Rect, e AnnotEdit) (int64, error) {
	f.mtx.Lock()

	pdf := C.gopdf_as_pdf(f.ctx, f.doc)
	if pdf == nil {
		f.mtx.Unlock()
		return 0, fmt.Errorf("fitz: create annot: %w", ErrNotPDF)
	}
	var b errBuf
	rect4 := [4]C.float{C.float(rect.X0), C.float(rect.Y0), C.float(rect.X1), C.float(rect.Y1)}
	var id C.int64_t
	rc := C.gopdf_annot_create(f.ctx, f.doc, C.int(pageNumber), C.int(t), &rect4[0],
		C.int(boolToInt(e.Rect != nil)), &id, b.ptr(), b.len())
	if rc != 0 {
		f.mtx.Unlock()
		return 0, opErr("create annot", &b, rc)
	}
	f.mtx.Unlock()

	newID := int64(id)
	if err := f.EditAnnot(pageNumber, newID, e); err != nil {
		_ = f.DeleteAnnot(pageNumber, newID)
		return 0, err
	}
	return newID, nil
}

// EditAnnot applies e to the annotation with the given ID and regenerates
// appearances. ErrStaleRef means the annotation no longer exists (deleted,
// or the document was edited underneath the caller).
func (f *Document) EditAnnot(pageNumber int, id int64, e AnnotEdit) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	var rect4 [4]C.float
	setRect := 0
	if e.Rect != nil {
		rect4 = [4]C.float{C.float(e.Rect.X0), C.float(e.Rect.Y0), C.float(e.Rect.X1), C.float(e.Rect.Y1)}
		setRect = 1
	}
	var ccontents, cauthor *C.char
	if e.SetContents {
		ccontents = C.CString(e.Contents)
	}
	if e.SetAuthor {
		cauthor = C.CString(e.Author)
	}
	defer func() {
		if ccontents != nil {
			C.free(unsafe.Pointer(ccontents))
		}
		if cauthor != nil {
			C.free(unsafe.Pointer(cauthor))
		}
	}()

	ccolorArr := make([]C.float, len(e.Color))
	for i, v := range e.Color {
		ccolorArr[i] = C.float(v)
	}
	var ccolor *C.float
	if len(ccolorArr) > 0 {
		ccolor = &ccolorArr[0]
	}

	quads := make([]C.fz_quad, len(e.QuadPoints))
	for i, q := range e.QuadPoints {
		quads[i] = C.fz_quad{
			ul: C.fz_point{x: C.float(q.UL.X), y: C.float(q.UL.Y)},
			ur: C.fz_point{x: C.float(q.UR.X), y: C.float(q.UR.Y)},
			ll: C.fz_point{x: C.float(q.LL.X), y: C.float(q.LL.Y)},
			lr: C.fz_point{x: C.float(q.LR.X), y: C.float(q.LR.Y)},
		}
	}
	var cquads *C.fz_quad
	if len(quads) > 0 {
		cquads = &quads[0]
	}

	setOpacity, opacity := 0, C.float(0)
	if e.Opacity != nil {
		setOpacity, opacity = 1, C.float(*e.Opacity)
	}
	setBorder, border := 0, C.float(0)
	if e.BorderWidth != nil {
		setBorder, border = 1, C.float(*e.BorderWidth)
	}

	var b errBuf
	rc := C.gopdf_annot_edit(f.ctx, f.doc, C.int(pageNumber), C.int64_t(id),
		C.int(setRect), &rect4[0],
		C.int(boolToInt(e.SetContents)), ccontents,
		C.int(boolToInt(e.SetAuthor)), cauthor,
		C.int(boolToInt(e.Color != nil)), ccolor, C.int(len(ccolorArr)),
		C.int(setOpacity), opacity,
		C.int(setBorder), border,
		C.int(boolToInt(e.QuadPoints != nil)), cquads, C.int(len(quads)),
		b.ptr(), b.len())
	return opErr("edit annot", &b, rc)
}

// DeleteAnnot removes the annotation with the given ID.
func (f *Document) DeleteAnnot(pageNumber int, id int64) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_annot_delete(f.ctx, f.doc, C.int(pageNumber), C.int64_t(id), b.ptr(), b.len())
	return opErr("delete annot", &b, rc)
}

// RefreshAnnots regenerates every appearance stream on the page (call after
// bulk changes made by other means).
func (f *Document) RefreshAnnots(pageNumber int) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_annots_refresh(f.ctx, f.doc, C.int(pageNumber), b.ptr(), b.len())
	return opErr("refresh annots", &b, rc)
}

// Fields lists the form widgets on one page.
func (f *Document) Fields(pageNumber int) ([]Field, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	pdf := C.gopdf_as_pdf(f.ctx, f.doc)
	if pdf == nil {
		return nil, fmt.Errorf("fitz: fields: %w", ErrNotPDF)
	}
	var b errBuf
	page := C.gopdf_load_page(f.ctx, pdf, C.int(pageNumber), b.ptr(), b.len())
	if page == nil {
		return nil, opErr("fields", &b, -1)
	}
	defer C.pdf_drop_page(f.ctx, page)

	var out []Field
	var cur *C.struct_pdf_annot
	for {
		var (
			id    C.int64_t
			wtype C.int
			rect  [4]C.float
			label *C.char
			value *C.char
		)
		rc := C.gopdf_widget_step(f.ctx, page, &cur, &id, &wtype, &rect[0], &label, &value, b.ptr(), b.len())
		if rc == 0 {
			break
		}
		if rc < 0 {
			return nil, opErr("fields", &b, rc)
		}
		fld := Field{
			ID:   int64(id),
			Type: WidgetType(wtype),
			Rect: Rect{float64(rect[0]), float64(rect[1]), float64(rect[2]), float64(rect[3])},
		}
		if label != nil {
			fld.Label = C.GoString(label)
			C.drop_string(f.ctx, label)
		}
		if value != nil {
			fld.Value = C.GoString(value)
			C.drop_string(f.ctx, value)
		}
		out = append(out, fld)
	}
	return out, nil
}

// SetFieldValue writes value to the field with the given ID and regenerates
// its appearance: text for text fields, "Yes"/"Off" (or the export value)
// for checkboxes and radios, an option string for choices.
func (f *Document) SetFieldValue(pageNumber int, id int64, value string) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	cval := C.CString(value)
	defer C.free(unsafe.Pointer(cval))
	rc := C.gopdf_field_set_value(f.ctx, f.doc, C.int(pageNumber), C.int64_t(id), cval, b.ptr(), b.len())
	return opErr("set field value", &b, rc)
}

// ResetField restores the field's default value.
func (f *Document) ResetField(pageNumber int, id int64) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_field_reset(f.ctx, f.doc, C.int(pageNumber), C.int64_t(id), b.ptr(), b.len())
	return opErr("reset field", &b, rc)
}

// SetMetadata writes a document-info entry. Keys follow fz_lookup_metadata
// conventions: "info:Title", "info:Author", "info:Subject",
// "info:Keywords", "info:Creator", "info:Producer", "info:CreationDate",
// "info:ModDate".
func (f *Document) SetMetadata(key, value string) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	ckey := C.CString(key)
	defer C.free(unsafe.Pointer(ckey))
	cvalue := C.CString(value)
	defer C.free(unsafe.Pointer(cvalue))
	rc := C.gopdf_set_metadata(f.ctx, f.doc, ckey, cvalue, b.ptr(), b.len())
	return opErr("set metadata", &b, rc)
}

// AddRedactAnnot marks rect for redaction. ApplyRedactions consumes all
// marked regions on the page.
func (f *Document) AddRedactAnnot(pageNumber int, rect Rect) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rect4 := [4]C.float{C.float(rect.X0), C.float(rect.Y0), C.float(rect.X1), C.float(rect.Y1)}
	rc := C.gopdf_redact_add(f.ctx, f.doc, C.int(pageNumber), &rect4[0], b.ptr(), b.len())
	return opErr("add redact annot", &b, rc)
}

// ApplyRedactions removes every redacted region on the page: text under the
// regions is deleted, covered image pixels blacked out, covered line art
// removed, and (when blackBoxes) a solid black box drawn in place.
func (f *Document) ApplyRedactions(pageNumber int, blackBoxes bool) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_redact_apply(f.ctx, f.doc, C.int(pageNumber), gBool(blackBoxes), b.ptr(), b.len())
	return opErr("apply redactions", &b, rc)
}

// LayerConfigs lists the document's optional-content configurations (named
// layer states). Empty for documents without layers.
func (f *Document) LayerConfigs() ([]LayerConfig, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	var b errBuf
	n := C.gopdf_layer_config_count(f.ctx, f.doc, b.ptr(), b.len())
	if n < 0 {
		return nil, opErr("layer configs", &b, n)
	}
	if n == 0 {
		return nil, nil
	}
	out := make([]LayerConfig, n)
	for i := 0; i < int(n); i++ {
		var name, creator *C.char
		rc := C.gopdf_layer_config_info(f.ctx, f.doc, C.int(i), &name, &creator, b.ptr(), b.len())
		if rc != 0 {
			return nil, opErr("layer configs", &b, rc)
		}
		if name != nil {
			out[i].Name = C.GoString(name)
			C.drop_string(f.ctx, name)
		}
		if creator != nil {
			out[i].Creator = C.GoString(creator)
			C.drop_string(f.ctx, creator)
		}
	}
	return out, nil
}

// SelectLayerConfig switches the document to optional-content configuration
// i. Subsequent renders reflect the new layer state.
func (f *Document) SelectLayerConfig(i int) error {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	var b errBuf
	rc := C.gopdf_layer_config_select(f.ctx, f.doc, C.int(i), b.ptr(), b.len())
	return opErr("select layer config", &b, rc)
}
