#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""LuminaGallery 人脸代理

在电脑上跑检测与特征提取，把结果回传给路由器。路由器只负责存储、聚类和展示，
不参与任何推理 —— 这是 1.2GHz 四核跑不动人脸识别时最实际的解法。

依赖:
    pip install opencv-python-headless requests numpy
模型 (放在 --models 目录):
    face_detection_yunet_2023mar.onnx
    face_recognition_sface_2021dec.onnx

用法:
    python face_agent.py --host http://192.168.2.1:8088 --user lwm --password ****** \
        --models ../.tools/models
"""

import argparse
import os
import time

import cv2
import numpy as np
import requests


def main():
    ap = argparse.ArgumentParser(description='LuminaGallery face agent')
    ap.add_argument('--host', default='http://192.168.2.1:8088')
    ap.add_argument('--user', default='lwm')
    ap.add_argument('--password', default=os.environ.get('LUMINA_FACE_PASSWORD'), help='也可通过 LUMINA_FACE_PASSWORD 环境变量提供')
    ap.add_argument('--models', default='../.tools/models')
    ap.add_argument('--limit', type=int, default=30, help='每批取多少张')
    ap.add_argument('--rounds', type=int, default=200, help='最多跑多少批')
    ap.add_argument('--min-size', type=int, default=24, help='忽略小于该像素的人脸')
    ap.add_argument("--watch", type=int, default=0, help="轮询间隔秒数，0 表示处理一次后退出")
    args = ap.parse_args()
    if not args.password:
        ap.error('请提供 --password 或 LUMINA_FACE_PASSWORD')
    cv2.setNumThreads(2)

    det = cv2.FaceDetectorYN.create(
        f"{args.models}/face_detection_yunet_2023mar.onnx", "", (320, 320), 0.85, 0.3, 5000)
    rec = cv2.FaceRecognizerSF.create(
        f"{args.models}/face_recognition_sface_2021dec.onnx", "")

    s = requests.Session()
    r = s.post(args.host + '/api/login',
               json={'name': args.user, 'password': args.password}, timeout=15)
    if not r.ok:
        raise SystemExit(f'登录失败: {r.status_code} {r.text[:120]}')
    print(f"已连接 {args.host}，登录为 {args.user}")

    done = 0
    faces_total = 0
    t0 = time.time()

    import itertools
    changed = False
    for _ in (itertools.count() if args.watch else range(args.rounds)):
        try:
            response = s.get(args.host + '/api/faces/pending',
                             params={'limit': args.limit}, timeout=30)
            if response.status_code == 401:
                s.post(args.host + '/api/login', json={'name': args.user, 'password': args.password}, timeout=15).raise_for_status()
                response = s.get(args.host + '/api/faces/pending', params={'limit': args.limit}, timeout=30)
            response.raise_for_status()
            j = response.json()
        except requests.RequestException as exc:
            if not args.watch:
                raise
            print(f'连接暂不可用，稍后重试：{type(exc).__name__}', flush=True)
            time.sleep(max(15, args.watch))
            continue
        ids = j.get('ids') or []
        if not ids:
            if args.watch > 0:
                if changed:
                    try:
                        s.post(args.host + "/api/people/rebuild", timeout=180).raise_for_status()
                        changed = False
                    except requests.RequestException:
                        print('归类暂不可用，下次重试', flush=True)
                time.sleep(max(15,args.watch))
                continue
            break
        for aid in ids:
            try:
                resp = s.get(f"{args.host}/t/{aid}/p.jpg", timeout=90)
                if not resp.ok:
                    print(f"暂不能读取 {aid}，下次重试", flush=True)
                    continue
                img = cv2.imdecode(np.frombuffer(resp.content, np.uint8), cv2.IMREAD_COLOR)
                if img is None:
                    print(f"暂不能读取 {aid}，下次重试", flush=True)
                    continue

                h, w = img.shape[:2]
                det.setInputSize((w, h))
                _, found = det.detect(img)
                payload = []
                if found is not None:
                    for f in found:
                        x, y, bw, bh = float(f[0]), float(f[1]), float(f[2]), float(f[3])
                        if bw < args.min_size or bh < args.min_size:
                            continue
                        aligned = rec.alignCrop(img, f)
                        feat = rec.feature(aligned).flatten().astype(np.float32)
                        payload.append({
                            'x': x / w, 'y': y / h, 'w': bw / w, 'h': bh / h,
                            'emb': feat.tolist(),
                        })
                s.post(f"{args.host}/api/faces/{aid}",
                       json={'faces': payload}, timeout=30).raise_for_status()
                faces_total += len(payload)
                done += 1
                changed = True
                if payload:
                    print(f"  [{done}] asset {aid}: {len(payload)} 张人脸", flush=True)
            except Exception as exc:  # noqa: BLE001
                print(f"  asset {aid} 失败: {exc}", flush=True)

        if args.watch:
            # Bound CPU use and avoid repeatedly requesting an unreadable image.
            if changed:
                try:
                    s.post(args.host + '/api/people/rebuild', timeout=180).raise_for_status()
                    changed = False
                except requests.RequestException:
                    print('归类暂不可用，下次重试', flush=True)
            print(f'本轮结束，累计处理 {done} 张照片；{max(15,args.watch)} 秒后检查新增照片', flush=True)
            time.sleep(max(15, args.watch))

    dt = time.time() - t0
    rate = done / dt if dt > 0 else 0
    print(f"完成：{done} 张照片，{faces_total} 张人脸，{dt:.1f}s（{rate:.1f} 张/秒）")

    j = s.post(args.host + '/api/people/rebuild', timeout=180).json()
    print('聚类结果:', j)


if __name__ == '__main__':
    main()
