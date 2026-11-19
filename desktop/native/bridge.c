//go:build desktopjni

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

static void fail(JNIEnv *env, const char *message) {
    jclass type = (*env)->FindClass(env, "java/lang/IllegalStateException");
    if (type) (*env)->ThrowNew(env, type, message);
}

JNIEXPORT jlong JNICALL Java_dev_hole_desktop_bridge_NativeCore_open(JNIEnv *env, jobject self) {
    (void)env; (void)self;
    return (jlong)hole_open();
}

JNIEXPORT jbyteArray JNICALL Java_dev_hole_desktop_bridge_NativeCore_call(
    JNIEnv *env, jobject self, jlong handle, jstring operation, jbyteArray input) {
    (void)self;
    if (!operation || !input) { fail(env, "Missing native argument"); return NULL; }
    jsize count = (*env)->GetArrayLength(env, input);
    if (count > 128 * 1024) { fail(env, "Request exceeds 128 KiB"); return NULL; }
    const char *op = (*env)->GetStringUTFChars(env, operation, NULL);
    if (!op) return NULL;
    char *buffer = malloc((size_t)count + 1);
    if (!buffer) { (*env)->ReleaseStringUTFChars(env, operation, op); fail(env, "Native allocation failed"); return NULL; }
    (*env)->GetByteArrayRegion(env, input, 0, count, (jbyte *)buffer);
    if ((*env)->ExceptionCheck(env)) { free(buffer); (*env)->ReleaseStringUTFChars(env, operation, op); return NULL; }
    char *output = hole_call((uint64_t)handle, (char *)op, buffer, count);
    free(buffer);
    (*env)->ReleaseStringUTFChars(env, operation, op);
    if (!output) { fail(env, "Native call failed"); return NULL; }
    size_t size = strlen(output);
    jbyteArray result = (*env)->NewByteArray(env, (jsize)size);
    if (result) (*env)->SetByteArrayRegion(env, result, 0, (jsize)size, (jbyte *)output);
    free(output);
    return result;
}

JNIEXPORT void JNICALL Java_dev_hole_desktop_bridge_NativeCore_close(JNIEnv *env, jobject self, jlong handle) {
    (void)env; (void)self;
    hole_close((uint64_t)handle);
}

JNIEXPORT void JNICALL Java_dev_hole_desktop_bridge_NativeCore_push(
    JNIEnv *env, jobject self, jlong handle, jlong sequence, jlong timestamp, jbyteArray input) {
    (void)self;
    if (!input) { fail(env, "Missing PCM"); return; }
    jsize count = (*env)->GetArrayLength(env, input);
    if (count < 1920 || count > 7680 || count % 1920) { fail(env, "Invalid PCM frame size"); return; }
    jbyte buffer[7680];
    (*env)->GetByteArrayRegion(env, input, 0, count, buffer);
    if ((*env)->ExceptionCheck(env)) return;
    if (hole_push((uint64_t)handle, sequence, timestamp, buffer, count)) fail(env, "Voice capture failed");
}

JNIEXPORT jbyteArray JNICALL Java_dev_hole_desktop_bridge_NativeCore_pull(JNIEnv *env, jobject self, jlong handle) {
    (void)self;
    jbyte buffer[7680];
    int size = hole_pull((uint64_t)handle, buffer, sizeof(buffer));
    if (size < 0) { fail(env, "Voice playback failed"); return NULL; }
    jbyteArray result = (*env)->NewByteArray(env, size);
    if (result && size) (*env)->SetByteArrayRegion(env, result, 0, size, buffer);
    return result;
}
