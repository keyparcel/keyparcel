#include "clipboard.h"
#include <string.h>

// Stream bounded chunks: GTK 4.22's stock provider can issue an oversized X11
// property write, and its string serializer can block the main loop on close.
typedef struct { GdkContentProvider parent; GBytes *bytes; } PlainText;
typedef struct { GdkContentProviderClass parent; } PlainTextClass;
G_DEFINE_TYPE(PlainText, plain_text, GDK_TYPE_CONTENT_PROVIDER)

static GdkContentFormats *formats(GdkContentProvider *provider) {
    (void)provider;
    const char *types[] = {"text/plain;charset=utf-8"};
    return gdk_content_formats_new(types, 1);
}
static void spliced(GObject *stream, GAsyncResult *result, gpointer data) {
    GTask *task = data;
    GError *error = NULL;
    if (g_output_stream_splice_finish(G_OUTPUT_STREAM(stream), result, &error) < 0)
        g_task_return_error(task, error);
    else g_task_return_boolean(task, TRUE);
    g_object_unref(task);
}
static void write_text(GdkContentProvider *provider, const char *mime,
        GOutputStream *output, int priority, GCancellable *cancel,
        GAsyncReadyCallback callback, gpointer data) {
    GTask *task = g_task_new(provider, cancel, callback, data);
    g_task_set_priority(task, priority);
    if (strcmp(mime, "text/plain;charset=utf-8") != 0) {
        g_task_return_new_error(task, G_IO_ERROR, G_IO_ERROR_NOT_SUPPORTED, "Plain UTF-8 text only");
        g_object_unref(task);
        return;
    }
    GInputStream *input = g_memory_input_stream_new_from_bytes(((PlainText *)provider)->bytes);
    g_task_set_task_data(task, input, g_object_unref);
    g_output_stream_splice_async(output, input, G_OUTPUT_STREAM_SPLICE_CLOSE_SOURCE,
        priority, cancel, spliced, task);
}
static gboolean written(GdkContentProvider *provider, GAsyncResult *result, GError **error) {
    g_return_val_if_fail(g_task_is_valid(result, provider), FALSE);
    return g_task_propagate_boolean(G_TASK(result), error);
}
static void finalize(GObject *object) {
    g_bytes_unref(((PlainText *)object)->bytes);
    G_OBJECT_CLASS(plain_text_parent_class)->finalize(object);
}
static void plain_text_class_init(PlainTextClass *class) {
    G_OBJECT_CLASS(class)->finalize = finalize;
    GdkContentProviderClass *provider = GDK_CONTENT_PROVIDER_CLASS(class);
    provider->ref_formats = formats;
    provider->ref_storable_formats = formats;
    provider->write_mime_type_async = write_text;
    provider->write_mime_type_finish = written;
}
static void plain_text_init(PlainText *text) { (void)text; }
GdkContentProvider *plain_text_provider(const char *text) {
    PlainText *provider = g_object_new(plain_text_get_type(), NULL);
    provider->bytes = g_bytes_new(text, strlen(text));
    return GDK_CONTENT_PROVIDER(provider);
}
