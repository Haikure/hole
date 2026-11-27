//go:build minecraftjni

#include <jni.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

static JavaVM *android_vm;
static jobject android_bridge;
static jmethodID android_call;

static void fail(JNIEnv *env, const char *message) {
    jclass cls = (*env)->FindClass(env, "java/lang/IllegalStateException");
    if (cls) (*env)->ThrowNew(env, cls, message);
}

// FCL supplies references owned by ART, never by the game's OpenJDK VM.
// Attach only for this call, and never pass jobjects between the two VMs.
static int initialize_android(const unsigned char *dex, jsize size) {
#ifdef __ANDROID__
    if (android_bridge) return 1;
    const char *vm_text = getenv("DALVIK_JAVAVM");
    const char *app_text = getenv("DALVIK_APPLICATION");
    if (!vm_text || !app_text || !size) return 0;
    JavaVM *vm = (JavaVM *)(uintptr_t)strtoull(vm_text, NULL, 10);
    jobject app = (jobject)(uintptr_t)strtoull(app_text, NULL, 16);
    if (!vm || !app) return 0;
    JNIEnv *env = NULL;
    int attached = (*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK;
    if (attached && (*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK) return 0;
    int ok = 0;
    if ((*env)->PushLocalFrame(env, 32) != JNI_OK) goto done;
    jclass buffer_cls = (*env)->FindClass(env, "java/nio/ByteBuffer");
    jclass loader_cls = (*env)->FindClass(env, "java/lang/ClassLoader");
    jclass dex_cls = (*env)->FindClass(env, "dalvik/system/InMemoryDexClassLoader");
    if ((*env)->ExceptionCheck(env) || !buffer_cls || !loader_cls || !dex_cls) goto frame;
    jmethodID wrap = (*env)->GetStaticMethodID(env, buffer_cls, "wrap", "([B)Ljava/nio/ByteBuffer;");
    jmethodID system = (*env)->GetStaticMethodID(env, loader_cls, "getSystemClassLoader", "()Ljava/lang/ClassLoader;");
    jmethodID construct = (*env)->GetMethodID(env, dex_cls, "<init>", "(Ljava/nio/ByteBuffer;Ljava/lang/ClassLoader;)V");
    jmethodID load = (*env)->GetMethodID(env, loader_cls, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;");
    if ((*env)->ExceptionCheck(env) || !wrap || !system || !construct || !load) goto frame;
    jbyteArray data = (*env)->NewByteArray(env, size);
    if (!data) goto frame;
    (*env)->SetByteArrayRegion(env, data, 0, size, (const jbyte *)dex);
    jobject buffer = (*env)->CallStaticObjectMethod(env, buffer_cls, wrap, data);
    jobject parent = (*env)->CallStaticObjectMethod(env, loader_cls, system);
    if ((*env)->ExceptionCheck(env)) goto frame;
    jobject loader = (*env)->NewObject(env, dex_cls, construct, buffer, parent);
    if ((*env)->ExceptionCheck(env) || !loader) goto frame;
    jstring name = (*env)->NewStringUTF(env, "dev.hole.forge.android.NetworkBridge");
    jclass cls = (*env)->CallObjectMethod(env, loader, load, name);
    if ((*env)->ExceptionCheck(env) || !cls) goto frame;
    jmethodID ctor = (*env)->GetMethodID(env, cls, "<init>", "(Landroid/content/Context;)V");
    jmethodID call = (*env)->GetMethodID(env, cls, "call", "(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;");
    if ((*env)->ExceptionCheck(env) || !ctor || !call) goto frame;
    jobject bridge = (*env)->NewObject(env, cls, ctor, app);
    if ((*env)->ExceptionCheck(env) || !bridge) goto frame;
    // Retain the platform adapter for the native library lifetime. Audio is
    // explicitly stopped; cancelled DNS calls may outlive Engine.close.
    android_bridge = (*env)->NewGlobalRef(env, bridge);
    android_vm = vm;
    android_call = call;
    ok = android_bridge != NULL;
frame:
    (*env)->PopLocalFrame(env, NULL);
done:
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    if (attached) (*vm)->DetachCurrentThread(vm);
    return ok;
#else
    return 0;
#endif
}

char *hole_android_call(char *operation, char *input) {
    if (!android_vm || !android_bridge) return NULL;
    JNIEnv *env = NULL;
    int attached = (*android_vm)->GetEnv(android_vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK;
#ifdef __ANDROID__
    if (attached && (*android_vm)->AttachCurrentThread(android_vm, &env, NULL) != JNI_OK) return NULL;
#else
    if (attached && (*android_vm)->AttachCurrentThread(android_vm, (void **)&env, NULL) != JNI_OK) return NULL;
#endif
    char *copy = NULL;
    if ((*env)->PushLocalFrame(env, 8) != JNI_OK) goto done;
    jstring op = (*env)->NewStringUTF(env, operation), in = (*env)->NewStringUTF(env, input);
    if ((*env)->ExceptionCheck(env) || !op || !in) goto frame;
    jstring value = (*env)->CallObjectMethod(env, android_bridge, android_call, op, in);
    if ((*env)->ExceptionCheck(env) || !value) goto frame;
    const char *text = (*env)->GetStringUTFChars(env, value, NULL);
    if (text) { copy = strdup(text); (*env)->ReleaseStringUTFChars(env, value, text); }
frame:
    (*env)->PopLocalFrame(env, NULL);
done:
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    if (attached) (*android_vm)->DetachCurrentThread(android_vm);
    return copy;
}

JNIEXPORT jlong JNICALL Java_dev_hole_forge_NativeCore_open(JNIEnv *env, jclass cls, jbyteArray dex) {
    int android = 0;
#ifdef __ANDROID__
    android = 1;
    if (!dex || (*env)->GetArrayLength(env, dex) > 1024*1024) { fail(env, "Missing Android adapter"); return 0; }
    jsize size = (*env)->GetArrayLength(env, dex);
    jbyte *bytes = (*env)->GetByteArrayElements(env, dex, NULL);
    if (!bytes) return 0;
    int ok = initialize_android((const unsigned char *)bytes, size);
    (*env)->ReleaseByteArrayElements(env, dex, bytes, JNI_ABORT);
    if (!ok) { fail(env, "FCL must provide DALVIK_JAVAVM and DALVIK_APPLICATION; update FCL."); return 0; }
#endif
    return (jlong)hole_mc_open(android);
}

JNIEXPORT jbyteArray JNICALL Java_dev_hole_forge_NativeCore_call(JNIEnv *env, jclass cls, jlong handle, jstring operation, jbyteArray input) {
    if (!operation || !input) { fail(env,"Missing native argument"); return NULL; }
    jsize size = (*env)->GetArrayLength(env,input);
    if (size > 128*1024) { fail(env,"Request exceeds 128 KiB"); return NULL; }
    const char *op = (*env)->GetStringUTFChars(env,operation,NULL);
    if (!op) return NULL;
    char *bytes = malloc((size_t)size+1);
    if (!bytes) { (*env)->ReleaseStringUTFChars(env,operation,op); fail(env,"Native allocation failed"); return NULL; }
    (*env)->GetByteArrayRegion(env,input,0,size,(jbyte *)bytes);
    if ((*env)->ExceptionCheck(env)) { free(bytes); (*env)->ReleaseStringUTFChars(env,operation,op); return NULL; }
    char *output = hole_mc_call((uint64_t)handle,(char *)op,bytes,size);
    free(bytes); (*env)->ReleaseStringUTFChars(env,operation,op);
    if (!output) { fail(env,"Native bridge failed"); return NULL; }
    size_t count = strlen(output);
    jbyteArray result = (*env)->NewByteArray(env,(jsize)count);
    if (result) (*env)->SetByteArrayRegion(env,result,0,(jsize)count,(jbyte *)output);
    free(output);
    return result;
}
JNIEXPORT void JNICALL Java_dev_hole_forge_NativeCore_close(JNIEnv *env, jclass cls, jlong handle) { hole_mc_close((uint64_t)handle); }
